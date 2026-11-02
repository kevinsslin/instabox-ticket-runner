package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func t3TestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE orchestration_v2_projection_runs(run_id TEXT,thread_id TEXT,ordinal INTEGER,status TEXT);
 CREATE TABLE orchestration_v2_projection_runtime_requests(status TEXT);
 CREATE TABLE orchestration_v2_effect_outbox(status TEXT);
 CREATE TABLE scheduled_tasks(enabled INTEGER);
 CREATE TABLE orchestration_v2_thread_launch_workflows(status TEXT);
 CREATE TABLE orchestration_events(sequence INTEGER, application_event_version INTEGER DEFAULT 2,aggregate_kind TEXT DEFAULT 'thread');
 CREATE TABLE orchestration_v2_projection_metadata(projection_name TEXT,last_sequence INTEGER);
 INSERT INTO orchestration_v2_projection_runs VALUES('run-1','thread-1',1,'completed');
 INSERT INTO orchestration_v2_projection_metadata VALUES('thread-projections',0);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
func TestT3IdleGuard(t *testing.T) {
	cases := []struct {
		name, mutation string
		busy           bool
	}{
		{"completed", "", false},
		{"running", `UPDATE orchestration_v2_projection_runs SET status='running'`, true},
		{"queued", `INSERT INTO orchestration_v2_projection_runs VALUES('run-2','thread-1',2,'queued')`, true},
		{"another human thread", `INSERT INTO orchestration_v2_projection_runs VALUES('run-2','thread-2',1,'starting')`, true},
		{"approval", `INSERT INTO orchestration_v2_projection_runtime_requests VALUES('pending')`, true},
		{"answered", `INSERT INTO orchestration_v2_projection_runtime_requests VALUES('resolved')`, false},
		{"finalization", `INSERT INTO orchestration_v2_effect_outbox VALUES('running')`, true},
		{"queued effect", `INSERT INTO orchestration_v2_effect_outbox VALUES('pending')`, true},
		{"unknown future state", `UPDATE orchestration_v2_projection_runs SET status='new-state'`, true},
		{"schedule", `INSERT INTO scheduled_tasks VALUES(1)`, true},
		{"launch in progress", `INSERT INTO orchestration_v2_thread_launch_workflows VALUES('preparing')`, true},
		{"unapplied event", `INSERT INTO orchestration_events(sequence) VALUES(1)`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := t3TestDB(t)
			if c.mutation != "" {
				if _, err := db.Exec(c.mutation); err != nil {
					t.Fatal(err)
				}
			}
			v, err := inspectT3DB(db, "thread-1")
			if err != nil || v.Busy != c.busy {
				t.Fatalf("%+v %v", v, err)
			}
		})
	}
}
func TestT3UnknownSchemaAndMissingRunPreserveWork(t *testing.T) {
	db := t3TestDB(t)
	if _, err := inspectT3DB(db, "missing"); err == nil {
		t.Fatal("missing launch assumed idle")
	}
	db.Exec("DROP TABLE orchestration_v2_effect_outbox")
	if _, err := inspectT3DB(db, "thread-1"); err == nil {
		t.Fatal("unknown schema assumed idle")
	}
}

type t3Fake struct {
	*fakeClient
	response t3State
	err      error
}

func (f *t3Fake) PairT3(string, string) (string, error) {
	return `{"credential":"synthetic-pairing"}`, f.err
}

func (f *t3Fake) Shell(box, cmd string) (string, error) {
	if strings.Contains(cmd, "t3-guest quiesce") || strings.Contains(cmd, "t3-guest status") {
		if f.err != nil {
			return "", f.err
		}
		b, _ := json.Marshal(f.response)
		return string(b), nil
	}
	return f.fakeClient.Shell(box, cmd)
}
func TestT3CheckpointPreservesLeaseAndAuthOnGuardFailure(t *testing.T) {
	for _, kind := range []string{"busy", "failure", "wrong-thread", "not-stopped"} {
		t.Run(kind, func(t *testing.T) {
			r, f, _ := setup(t)
			seedTask(t, r, f)
			task, _ := r.State.Task("TASK-1")
			task.Backend = "t3"
			r.State.Save(task)
			tf := &t3Fake{fakeClient: f, response: t3State{Thread: task.Conversation, Run: "run-1", Stopped: true}}
			switch kind {
			case "busy":
				tf.response.Busy = true
			case "failure":
				tf.err = errors.New("guard unavailable")
			case "wrong-thread":
				tf.response.Thread = "other"
			case "not-stopped":
				tf.response.Stopped = false
			}
			r.Client = tf
			if _, err := r.Checkpoint(task.Ticket); err == nil {
				t.Fatal("unsafe checkpoint passed")
			}
			var lease string
			r.State.DB.QueryRow("SELECT ticket FROM leases WHERE user=?", task.User).Scan(&lease)
			if lease != task.Ticket || f.removed != 0 || f.boxes[task.Box].State != "running" {
				t.Fatal("guard error removed credentials or released lease")
			}
		})
	}
}
func TestT3CredentialSaveFailureAfterQuiescencePreservesGuest(t *testing.T) {
	r, f, m := setup(t)
	seedTask(t, r, f)
	task, _ := r.State.Task("TASK-1")
	task.Backend = "t3"
	r.State.Save(task)
	r.Client = &t3Fake{fakeClient: f, response: t3State{Thread: task.Conversation, Run: "run-1", Stopped: true}}
	m.fail = true
	if _, err := r.Checkpoint(task.Ticket); err == nil {
		t.Fatal("save failed but checkpoint passed")
	}
	var lease string
	r.State.DB.QueryRow("SELECT ticket FROM leases WHERE user=?", task.User).Scan(&lease)
	if lease != task.Ticket || f.removed != 0 || f.boxes[task.Box].State != "running" {
		t.Fatal("save failure discarded guest auth or lease")
	}
}

func TestT3PairingStaysInPrivateFile(t *testing.T) {
	r, f, _ := setup(t)
	seedTask(t, r, f)
	task, _ := r.State.Task("TASK-1")
	task.Backend = "t3"
	if err := r.State.Save(task); err != nil {
		t.Fatal(err)
	}
	r.Config.T3PublicURL = "https://bot.example"
	r.Client = &t3Fake{fakeClient: f}
	path := filepath.Join(t.TempDir(), "pair.json")
	result, err := t3Pair(r, task.Ticket, path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "synthetic-pairing") {
		t.Fatal("pairing credential entered CLI response")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pairing file is not private")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "synthetic-pairing") {
		t.Fatal("private pairing not saved")
	}
	if _, err = t3Pair(r, task.Ticket, path); err == nil {
		t.Fatal("existing pairing overwritten")
	}
}
func TestT3ConfigAndTaskBackendPersist(t *testing.T) {
	c := Config{Provider: "codex", Backend: "t3", Base: "main", VCPUs: 2, Memory: 4096, T3PublicURL: "https://bot.example:8443"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Provider = "claude"
	if c.Validate() == nil {
		t.Fatal("unsupported T3 provider passed")
	}
	for _, bad := range []string{"http://bot.example", "https://u:p@host", "https://host/path", "https://host/?secret=value"} {
		if validT3Origin(bad) {
			t.Fatal(bad)
		}
	}
	r, f, _ := setup(t)
	seedTask(t, r, f)
	v, _ := r.State.Task("TASK-1")
	v.Backend = "t3"
	if err := r.State.Save(v); err != nil {
		t.Fatal(err)
	}
	got, err := r.State.Task(v.Ticket)
	if err != nil || got.Backend != "t3" {
		t.Fatal(got, err)
	}
}
func TestT3GatewayAuthOriginAndPausedResume(t *testing.T) {
	r, f, _ := setup(t)
	seedTask(t, r, f)
	v, _ := r.State.Task("TASK-1")
	v.Backend = "t3"
	v.Phase = "paused"
	r.State.Save(v)
	dir := t.TempDir()
	p := filepath.Join(dir, "password")
	os.WriteFile(p, []byte(strings.Repeat("p", 32)), 0600)
	digest, err := privatePassword(p)
	if err != nil {
		t.Fatal(err)
	}
	ui := &t3UI{root: r.State.Root, task: v.Ticket, origin: "https://bot.example", csrf: "csrf-test", password: digest}
	request := func(method, path, origin, body string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth {
			req.SetBasicAuth("operator", strings.Repeat("p", 32))
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, req)
		return w
	}
	if w := request("GET", "/", "", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/", "https://evil.example", "", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/environment/thread", "", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), "Resume in T3 Code") || strings.Contains(w.Body.String(), "Agent activity") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", "/_ib/resume", "https://bot.example", url.Values{"csrf": {"wrong"}}.Encode(), true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/_ib/resume", "", "csrf=csrf-test", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/_ib/resume", "", "", true); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}

func TestT3IdleProcessGuard(t *testing.T) {
	for _, cmd := range [][]string{{"/usr/bin/go", "test", "./..."}, {"/bin/bash"}, {"node", "server.js"}, {"apt-get", "install", "xvfb"}, {"python3", "background.py"}, {"codex", "exec", "work"}} {
		if idleT3Command(cmd) {
			t.Fatalf("active/unknown command allowed: %v", cmd)
		}
	}
	for _, cmd := range [][]string{{t3Bin, "serve"}, {"codex", "app-server"}, {"node", "/usr/local/bin/codex", "app-server"}, {"python3", "/usr/local/bin/instabox-mcp", "mcp"}} {
		if !idleT3Command(cmd) {
			t.Fatalf("known idle provider blocked: %v", cmd)
		}
	}
}

type t3ScriptClient struct {
	*fakeClient
	starts  int
	receipt string
}

func (c *t3ScriptClient) InstallT3Guest(string, string) error { return nil }
func (c *t3ScriptClient) Shell(box, cmd string) (string, error) {
	switch {
	case strings.Contains(cmd, "t3-guest project-exists"):
		return `{"exists":true}`, nil
	case strings.Contains(cmd, " thread start "):
		c.starts++
		return "", errors.New("connection lost")
	case strings.Contains(cmd, "cat "+t3Home+"/receipts/"):
		if c.receipt == "" {
			return "", errors.New("missing")
		}
		return c.receipt, nil
	case strings.Contains(cmd, "t3-guest status"):
		return `{"thread":"original","run":"original-run","busy":true}`, nil
	case strings.Contains(cmd, "thread show"):
		return `{"thread":{"threadId":"original","providerInstanceId":"codex","link":"http://127.0.0.1:3774/environment/original"}}`, nil
	}
	return c.fakeClient.Shell(box, cmd)
}
func TestT3UncertainLaunchNeverRedispatches(t *testing.T) {
	for _, receipt := range []string{"", `{"threadId":"original","runId":"original-run"}`} {
		t.Run(receipt, func(t *testing.T) {
			r, f, _ := setup(t)
			seedTask(t, r, f)
			task, _ := r.State.Task("TASK-1")
			task.Backend = "t3"
			task.Repo = "owner/repo"
			task.Conversation = ""
			r.State.Save(task)
			c := &t3ScriptClient{fakeClient: f, receipt: receipt}
			r.Client = c
			if _, err := r.Run(task.Ticket, "Fix the tests", "", "test-model"); err == nil {
				t.Fatal("lost start returned success")
			}
			if _, err := r.Run(task.Ticket, "Fix the tests", "", "test-model"); err == nil {
				t.Fatal("uncertain start redispatched")
			}
			if c.starts != 1 {
				t.Fatal("prompt replayed", c.starts)
			}
			var nonce string
			r.State.DB.QueryRow("SELECT nonce FROM t3_tasks WHERE ticket=?", task.Ticket).Scan(&nonce)
			if len(nonce) != 32 {
				t.Fatal("no durable launch nonce")
			}
			if receipt == "" {
				if _, err := r.RecoverTurn(task.Ticket, ""); err == nil {
					t.Fatal("missing receipt recovered without explicit selection")
				}
			}
			got, err := r.RecoverTurn(task.Ticket, "original")
			if err != nil || got.Conversation != "original" || got.Phase != "active" {
				t.Fatal(got, err)
			}
			var job, status string
			r.State.DB.QueryRow("SELECT job,status FROM turns WHERE ticket=?", task.Ticket).Scan(&job, &status)
			if job != "original-run" || status != "active" || c.starts != 1 {
				t.Fatal("incorrect turn recovery", job, status, c.starts)
			}
		})
	}
}

func TestT3ResumeArmsNextHumanRunWithoutReplayingOldCompletion(t *testing.T) {
	r, f, _ := setup(t)
	seedTask(t, r, f)
	task, _ := r.State.Task("TASK-1")
	task.Backend = "t3"
	r.State.Save(task)
	r.State.DB.Exec("INSERT INTO turns(ticket,conversation,job,mode,status) VALUES(?,?,?,'host','done')", task.Ticket, task.Conversation, "old-run")
	tf := &t3Fake{fakeClient: f, response: t3State{Thread: task.Conversation, Run: "old-run", Status: "completed"}}
	r.Client = tf
	if err := r.armT3(task); err != nil {
		t.Fatal(err)
	}
	var status, job string
	r.State.DB.QueryRow("SELECT status,job FROM turns WHERE ticket=?", task.Ticket).Scan(&status, &job)
	if status != "awaiting-input" || job != "old-run" {
		t.Fatal(status, job)
	}
	called := 0
	w := supervisor{state: r.State, client: tf, checkpoint: func(string) error { called++; return nil }}
	turn := supervisedTurn{Task: task.Ticket, Conversation: task.Conversation, Job: job, Status: status}
	if done, err := w.inspect(turn); err != nil || done || called != 0 {
		t.Fatal("old completed turn immediately repaused the VM", done, err, called)
	}
	tf.response.Run = "human-run"
	if done, err := w.inspect(turn); err != nil || !done || called != 1 {
		t.Fatal("new human run was not checkpointed", done, err, called)
	}
}
