package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

type linearSettings struct {
	Workspace  string   `yaml:"workspace_id"`
	AppUser    string   `yaml:"app_user_id"`
	Teams      []string `yaml:"team_ids"`
	Users      []string `yaml:"user_ids"`
	TokenFile  string   `yaml:"access_token_file"`
	SecretFile string   `yaml:"webhook_secret_file"`
	Dashboard  string   `yaml:"dashboard_url"`
}
type linearWebhook struct {
	Type         string `json:"type"`
	Action       string `json:"action"`
	Organization string `json:"organizationId"`
	AppUser      string `json:"appUserId"`
	Timestamp    int64  `json:"webhookTimestamp"`
	Prompt       string `json:"promptContext"`
	Session      struct {
		ID      string `json:"id"`
		Creator string `json:"creatorId"`
		Issue   struct {
			ID   string `json:"id"`
			Team string `json:"teamId"`
		} `json:"issue"`
	} `json:"agentSession"`
	Activity struct {
		ID      string `json:"id"`
		Session string `json:"agentSessionId"`
		User    string `json:"userId"`
		Signal  string `json:"signal"`
		Content struct {
			Type string `json:"type"`
			Body string `json:"body"`
		} `json:"content"`
	} `json:"agentActivity"`
}
type linearDelivery struct {
	ID, Session, Task, Prompt, Status, Reply, Job string
	Ack                                           int
}
type linearBridge struct {
	db           *sql.DB
	settings     linearSettings
	secret       []byte
	root, config string
	client       Client
	http         *http.Client
	endpoint     string
	dispatch     func(...string) (any, error)
	streams      *turnStreams
}

func privateText(path string) ([]byte, error) {
	info, e := os.Lstat(expandHome(path))
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return nil, errors.New("secret must be a private regular file, maximum 16 KiB")
	}
	raw, e := os.ReadFile(expandHome(path))
	if e != nil {
		return nil, errors.New("cannot read private secret file")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("empty secret file")
	}
	return raw, nil
}
func loadLinearSettings(path string) (linearSettings, error) {
	var s linearSettings
	raw, e := os.ReadFile(expandHome(path))
	if e != nil {
		return s, errors.New("cannot read private Linear settings")
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if e = d.Decode(&s); e != nil {
		return s, errors.New("invalid Linear settings")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return s, errors.New("use one Linear YAML document")
	}
	u, e := url.Parse(s.Dashboard)
	if s.Workspace == "" || s.AppUser == "" || len(s.Teams) == 0 || len(s.Users) == 0 || e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return s, errors.New("Linear requires workspace, app identity, team/user allowlists and a credential-free HTTPS dashboard URL")
	}
	return s, nil
}
func openLinearDB(root string) (*sql.DB, error) {
	path := filepath.Join(root, "linear.db")
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		f.Close()
		info, e = os.Lstat(path)
	}
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("Linear state must be a private regular file")
	}
	u := url.URL{Scheme: "file", Path: path}
	db, e := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(2000)")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS deliveries(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE NOT NULL,session TEXT NOT NULL,task TEXT NOT NULL,prompt TEXT NOT NULL,status TEXT NOT NULL,reply TEXT NOT NULL DEFAULT '',ack INTEGER NOT NULL DEFAULT 0,job TEXT NOT NULL DEFAULT '');`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func runLinear(root, config string, args []string) error {
	f := flag.NewFlagSet("linear", flag.ContinueOnError)
	settingsFile := f.String("settings", "", "private Linear settings YAML")
	listen := f.String("listen", "127.0.0.1:8788", "loopback webhook listener")
	resolve := f.String("resolve", "", "delivery ID to reconcile after controller recovery")
	if e := f.Parse(args); e != nil {
		return e
	}
	if len(f.Args()) != 0 {
		return errors.New("unexpected Linear arguments")
	}
	settings, e := loadLinearSettings(*settingsFile)
	if e != nil {
		return e
	}
	c, e := loadConfig(expandHome(config), os.LookupEnv)
	if e != nil {
		return e
	}
	if c.User == "" || c.Org == "" || c.Repo == "" {
		return errors.New("configure one fixed profile, org and repository for the Linear bridge")
	}
	// Initialize under the existing controller lock, then release it immediately.
	state, e := openState(expandHome(root))
	if e != nil {
		return e
	}
	root = state.Root
	state.Close()
	lock := flock.New(filepath.Join(root, "linear.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		return errors.New("Linear bridge already running; stop it before resolving a delivery")
	}
	defer lock.Unlock()
	db, e := openLinearDB(root)
	if e != nil {
		return e
	}
	defer db.Close()
	b := &linearBridge{db: db, settings: settings, root: root, config: config, client: Instabox{Timeout: 20 * time.Second, MaxOutput: 8 << 20}, http: &http.Client{Timeout: 8 * time.Second}, endpoint: "https://api.linear.app/graphql"}
	b.dispatch = func(cmd ...string) (any, error) {
		argv := []string{"--state", root}
		if config != "" {
			argv = append(argv, "--config", config)
		}
		return execute(append(argv, cmd...))
	}
	if *resolve != "" {
		return b.resolve(*resolve)
	}
	host, _, e := net.SplitHostPort(*listen)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("Linear webhook must bind to a loopback IP behind HTTPS")
	}
	if b.secret, e = privateText(settings.SecretFile); e != nil {
		return e
	}
	if len(b.secret) < 24 {
		return errors.New("Linear signing secret is too short")
	}
	if _, e = privateText(settings.TokenFile); e != nil {
		return e
	}
	// An interrupted mutating operation must never be replayed automatically.
	if _, e = db.Exec("UPDATE deliveries SET status='blocked',reply=? WHERE status='starting'", "Controller restarted during dispatch. Reconcile the task on the host; the prompt was not replayed."); e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); b.work(ctx) }()
	server := &http.Server{Addr: *listen, Handler: b, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 12 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	fmt.Fprintf(os.Stderr, "Linear webhook: http://%s/webhooks/linear (route only this path through HTTPS)\n", *listen)
	go func() { <-ctx.Done(); server.Close() }()
	e = server.ListenAndServe()
	cancel()
	<-done
	return e
}
func linearContains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func (b *linearBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/webhooks/linear" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10))
	if e != nil {
		http.Error(w, "Payload too large", 413)
		return
	}
	sig, e := hex.DecodeString(r.Header.Get("Linear-Signature"))
	mac := hmac.New(sha256.New, b.secret)
	mac.Write(raw)
	if e != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		http.Error(w, "Invalid signature", 401)
		return
	}
	var event linearWebhook
	if json.Unmarshal(raw, &event) != nil {
		http.Error(w, "Invalid payload", 400)
		return
	}
	skew := time.Now().UnixMilli() - event.Timestamp
	if skew < -60000 || skew > 60000 {
		http.Error(w, "Expired event", 401)
		return
	}
	if event.Type != "AgentSessionEvent" || (event.Action != "created" && event.Action != "prompted") {
		w.WriteHeader(204)
		return
	}
	if event.Organization != b.settings.Workspace || event.AppUser != b.settings.AppUser || !linearContains(b.settings.Teams, event.Session.Issue.Team) {
		http.Error(w, "Not permitted", 403)
		return
	}
	id := "created:" + event.Session.ID
	prompt := event.Prompt
	actor := event.Session.Creator
	if event.Action == "prompted" {
		id = "prompt:" + event.Activity.ID
		prompt = event.Activity.Content.Body
		actor = event.Activity.User
		if event.Activity.Content.Type != "prompt" || event.Activity.Session != event.Session.ID || event.Activity.ID == "" {
			http.Error(w, "Invalid prompt activity", 400)
			return
		}
	}
	if !linearContains(b.settings.Users, actor) || actor == b.settings.AppUser {
		http.Error(w, "Not permitted", 403)
		return
	}
	if event.Session.ID == "" || event.Session.Issue.ID == "" || strings.TrimSpace(prompt) == "" || len(prompt) > 256<<10 {
		http.Error(w, "Missing session, issue or prompt", 400)
		return
	}
	sum := sha256.Sum256([]byte(event.Organization + ":" + event.Session.Issue.ID))
	task := "LIN-" + hex.EncodeToString(sum[:12])
	status := "queued"
	reply := ""
	if event.Activity.Signal != "" {
		status = "blocked"
		reply = "Control signals are not executed automatically. Inspect or interrupt the native agent on the trusted host; no follow-up turn was started."
	}
	// Session IDs may not be rebound to a different issue, even across redelivery.
	tx, e := b.db.Begin()
	if e != nil {
		http.Error(w, "Inbox unavailable", 503)
		return
	}
	defer tx.Rollback()
	var previous string
	err := tx.QueryRow("SELECT task FROM deliveries WHERE session=? LIMIT 1", event.Session.ID).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		http.Error(w, "Inbox unavailable", 503)
		return
	}
	if err == nil && previous != task {
		http.Error(w, "Session mapping mismatch", 409)
		return
	}
	_, e = tx.Exec("INSERT OR IGNORE INTO deliveries(id,session,task,prompt,status,reply) VALUES(?,?,?,?,?,?)", id, event.Session.ID, task, prompt, status, reply)
	if e != nil {
		http.Error(w, "Inbox unavailable", 503)
		return
	}
	if e = tx.Commit(); e != nil {
		http.Error(w, "Inbox unavailable", 503)
		return
	}
	w.WriteHeader(200)
}
func (b *linearBridge) graphql(query string, variables any, out any) error {
	token, e := privateText(b.settings.TokenFile)
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req, e := http.NewRequest("POST", b.endpoint, bytes.NewReader(raw))
	if e != nil {
		return errors.New("invalid Linear endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	req.Header.Set("Content-Type", "application/json")
	response, e := b.http.Do(req)
	if e != nil {
		return errors.New("Linear request failed")
	}
	defer response.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil || len(raw) > 2<<20 || response.StatusCode != 200 {
		return errors.New("Linear response unavailable")
	}
	var envelope struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Errors) > 0 {
		return errors.New("Linear GraphQL operation failed")
	}
	if json.Unmarshal(raw, out) != nil {
		return errors.New("invalid Linear response")
	}
	return nil
}
func activityUUID(id, kind string) string {
	sum := sha256.Sum256([]byte(id + ":" + kind))
	sum[6] = (sum[6] & 15) | 64
	sum[8] = (sum[8] & 63) | 128
	h := hex.EncodeToString(sum[:16])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
func (b *linearBridge) activity(d linearDelivery, kind, body string) error {
	id := activityUUID(d.ID, kind)
	input := map[string]any{"id": id, "agentSessionId": d.Session, "content": map[string]string{"type": kind, "body": body}}
	var result struct {
		Data struct {
			Activity struct {
				Success bool `json:"success"`
			} `json:"agentActivityCreate"`
		} `json:"data"`
	}
	e := b.graphql(`mutation($input:AgentActivityCreateInput!){agentActivityCreate(input:$input){success}}`, map[string]any{"input": input}, &result)
	if e == nil && result.Data.Activity.Success {
		return nil
	}
	// A timeout may have committed. Verify the deterministic activity before retry.
	var existing struct {
		Data struct {
			Activity *struct {
				Session struct {
					ID string `json:"id"`
				} `json:"agentSession"`
			} `json:"agentActivity"`
		} `json:"data"`
	}
	if b.graphql(`query($id:String!){agentActivity(id:$id){agentSession{id}}}`, map[string]string{"id": id}, &existing) == nil && existing.Data.Activity != nil && existing.Data.Activity.Session.ID == d.Session {
		return nil
	}
	return errors.New("Linear activity was not confirmed")
}
func (b *linearBridge) link(d linearDelivery, pr string) error {
	links := []map[string]string{{"label": "Task monitor", "url": strings.TrimRight(b.settings.Dashboard, "/") + "/#" + url.PathEscape(d.Task)}}
	if pr != "" {
		links = append(links, map[string]string{"label": "Pull request", "url": pr})
	}
	var out struct {
		Data struct {
			Update struct {
				Success bool `json:"success"`
			} `json:"agentSessionUpdate"`
		} `json:"data"`
	}
	if e := b.graphql(`mutation($id:String!,$input:AgentSessionUpdateInput!){agentSessionUpdate(id:$id,input:$input){success}}`, map[string]any{"id": d.Session, "input": map[string]any{"addedExternalUrls": links}}, &out); e != nil {
		return e
	}
	if !out.Data.Update.Success {
		return errors.New("Linear session link not confirmed")
	}
	return nil
}
func (b *linearBridge) delivery(query string) (linearDelivery, error) {
	var d linearDelivery
	e := b.db.QueryRow(query).Scan(&d.ID, &d.Session, &d.Task, &d.Prompt, &d.Status, &d.Reply, &d.Ack, &d.Job)
	return d, e
}

const deliveryColumns = "id,session,task,prompt,status,reply,ack,job"

func (b *linearBridge) work(ctx context.Context) {
	b.streams = newTurnStreams(ctx)
	defer b.streams.close()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-b.streams.results:
			delete(b.streams.active, result.Key)
			if ctx.Err() != nil {
				return
			}
			if result.Err != nil {
				var d linearDelivery
				e := b.db.QueryRow("SELECT "+deliveryColumns+" FROM deliveries WHERE id=?", result.Key).Scan(&d.ID, &d.Session, &d.Task, &d.Prompt, &d.Status, &d.Reply, &d.Ack, &d.Job)
				if e == nil {
					b.block(d, "Event following or completion needs attention. Reconcile the original turn on the host; no prompt was replayed.")
				}
			}
			b.step()
		case <-tick.C:
			b.step()
		}
	}
}
func (b *linearBridge) step() {
	// Acknowledge receipts before waiting for a long-running turn.
	d, e := b.delivery("SELECT " + deliveryColumns + " FROM deliveries WHERE ack=0 ORDER BY seq LIMIT 1")
	if e == nil {
		if b.link(d, "") == nil && b.activity(d, "thought", "Request saved. It will run in the configured repository after earlier work completes. Follow progress in the task monitor.") == nil {
			b.db.Exec("UPDATE deliveries SET ack=1 WHERE id=?", d.ID)
		}
	}
	d, e = b.delivery("SELECT " + deliveryColumns + " FROM deliveries d WHERE ack>=1 AND status IN ('queued','running','finishing','blocked') AND (status='blocked' OR NOT EXISTS (SELECT 1 FROM deliveries earlier WHERE earlier.task=d.task AND earlier.seq<d.seq AND earlier.status!='done')) ORDER BY CASE WHEN status='blocked' THEN 0 WHEN status='running' THEN 1 ELSE 2 END,seq LIMIT 1")
	if e != nil {
		return
	}
	switch d.Status {
	case "queued":
		b.start(d)
	case "running":
		if b.streams == nil || b.streams.active[d.ID] {
			return
		}
		if d.Ack == 1 {
			started := d
			started.ID += ":started"
			if b.activity(started, "thought", "Agent turn is running. Follow-up prompts wait for this turn to finish.") == nil {
				b.db.Exec("UPDATE deliveries SET ack=2 WHERE id=?", d.ID)
			}
		}
		b.streams.start(d.ID, func(ctx context.Context) error {
			follower, ok := b.client.(eventFollower)
			if !ok {
				return errors.New("native event following unavailable")
			}
			return awaitCompletion(ctx, func() (bool, error) { return b.monitor(d) }, func(ctx context.Context) error {
				s, e := openObservationState(b.root)
				if e != nil {
					return e
				}
				t, e := s.Task(d.Task)
				s.DB.Close()
				if e != nil {
					return e
				}
				return follower.Follow(ctx, t.Box, t.Conversation)
			}, streamSleep)
		})
	case "finishing":
		if b.streams != nil && b.streams.active[d.ID] {
			return
		}
		b.finish(d)
	case "blocked":
		if b.activity(d, "error", d.Reply) == nil {
			b.db.Exec("UPDATE deliveries SET status='attention' WHERE id=?", d.ID)
		}
	}
}
func (b *linearBridge) block(d linearDelivery, message string) {
	b.db.Exec("UPDATE deliveries SET status='blocked',reply=? WHERE id=?", message, d.ID)
}
func (b *linearBridge) start(d linearDelivery) {
	// Commit intent before any mutating remote operation, never auto-replay it.
	result, e := b.db.Exec("UPDATE deliveries SET status='starting' WHERE id=? AND status='queued'", d.ID)
	if e != nil {
		return
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		return
	}
	if _, e = b.dispatch("create", "--task", d.Task); e != nil {
		b.block(d, "Dispatch needs attention. Inspect controller status and reconcile before retrying; no automatic replay.")
		return
	}
	state, e := openObservationState(b.root)
	if e != nil {
		b.block(d, "Task state unavailable. Inspect controller before retrying.")
		return
	}
	task, e := state.Task(d.Task)
	state.DB.Close()
	if e != nil {
		b.block(d, "Task state unavailable. Inspect controller before retrying.")
		return
	}
	if task.Repo == "" {
		if _, e = b.dispatch("prepare", d.Task); e != nil {
			b.block(d, "Repository preparation needs attention. Reconcile on the host before retrying.")
			return
		}
	}
	prompt := "Complete this task in the configured repository. Follow its instructions and verify your changes. Report actual test commands and results. Do not publish a PR unless explicitly authorized in the request.\n\n" + d.Prompt
	f, e := os.CreateTemp(b.root, "linear-prompt-")
	if e != nil {
		b.block(d, "Cannot create private prompt file.")
		return
	}
	path := f.Name()
	defer os.Remove(path)
	_, e = f.WriteString(prompt)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		b.block(d, "Cannot save private prompt file.")
		return
	}
	started, e := b.dispatch("run", d.Task, "--cwd", "/data/home/workspace", "--supervision", "external", "--prompt-file", path)
	if e != nil {
		b.block(d, "Agent start needs attention. A turn may already be running; inspect and recover it before retrying.")
		return
	}
	turn, ok := started.(StartResult)
	if !ok || turn.Job == "" {
		b.block(d, "Start response did not identify a job. Inspect and recover the original turn; do not replay the prompt.")
		return
	}
	b.db.Exec("UPDATE deliveries SET status='running',job=? WHERE id=?", turn.Job, d.ID)
}
func (b *linearBridge) monitor(d linearDelivery) (bool, error) {
	s, e := openObservationState(b.root)
	if e != nil {
		return false, errReconcileRetry
	}
	defer s.DB.Close()
	t, e := s.Task(d.Task)
	if e != nil {
		return false, e
	}
	r := Runner{State: s, Client: b.client}
	box, e := r.owned(t)
	if e != nil {
		if errors.Is(e, errOwnershipMismatch) {
			return false, e
		}
		return false, errReconcileRetry
	}
	if box.State != "running" {
		return false, errors.New("VM is no longer running; reconcile before resuming")
	}
	native, e := readNativeTurn(b.client, t, d.Job)
	if e != nil {
		return false, e
	}
	if native.Status == "running" {
		return false, nil
	}
	var events struct {
		Events []json.RawMessage `json:"events"`
	}
	detail := "Final event details are unavailable; inspect native logs on the trusted host."
	if b.client.Call(&events, "agent", "events", t.Box, "-c", t.Conversation) == nil {
		detail = agentReply(events.Events)
	}
	reply := fmt.Sprintf("Agent turn ended (exit %d). This is not a certification that tests passed.\n\n", *native.ExitCode) + detail
	if validPRURL(t.PR, t.Repo) {
		reply += "\n\nPR: " + t.PR
	}
	if _, e = b.db.Exec("UPDATE deliveries SET status='finishing',reply=? WHERE id=?", reply, d.ID); e != nil {
		return false, errReconcileRetry
	}
	d.Reply = reply
	b.finish(d)
	return true, nil
}
func agentReply(events []json.RawMessage) string {
	reply := "No final text was captured. Inspect the task monitor for native events and command results."
	for _, raw := range events {
		var event struct {
			Type   string `json:"type"`
			Result string `json:"result"`
			Item   struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &event) != nil {
			continue
		}
		if event.Type == "instabox.turn" {
			reply = "No final text was captured for this turn. Inspect the task monitor."
		}
		if event.Item.Type == "agent_message" && event.Item.Text != "" {
			reply = event.Item.Text
		}
		if event.Type == "assistant" {
			for _, c := range event.Message.Content {
				if c.Type == "text" && c.Text != "" {
					reply = c.Text
				}
			}
		}
		if event.Type == "result" && event.Result != "" {
			reply = event.Result
		}
	}
	if len(reply) > 12000 {
		reply = reply[:12000] + "\n[Response truncated; see task monitor]"
	}
	return reply
}
func (b *linearBridge) finish(d linearDelivery) {
	if _, e := b.dispatch("checkpoint", d.Task); e != nil {
		if e.Error() == "controller busy" {
			return
		}
		b.block(d, "Agent turn ended, but checkpoint needs attention. Work and lease were preserved. Inspect the host before continuing.\n\n"+d.Reply)
		return
	}
	s, e := openObservationState(b.root)
	if e != nil {
		return
	}
	t, e := s.Task(d.Task)
	s.DB.Close()
	if e != nil {
		return
	}
	pr := ""
	if validPRURL(t.PR, t.Repo) {
		pr = t.PR
	}
	if b.link(d, pr) != nil || b.activity(d, "response", d.Reply+"\n\nVM checkpointed and paused. Reply in this agent conversation to request a revision.") != nil {
		return
	}
	b.db.Exec("UPDATE deliveries SET status='done',prompt='' WHERE id=?", d.ID)
}
func (b *linearBridge) resolve(id string) error {
	var d linearDelivery
	e := b.db.QueryRow("SELECT "+deliveryColumns+" FROM deliveries WHERE id=?", id).Scan(&d.ID, &d.Session, &d.Task, &d.Prompt, &d.Status, &d.Reply, &d.Ack, &d.Job)
	if e != nil {
		return errors.New("unknown delivery")
	}
	if d.Status != "blocked" && d.Status != "attention" {
		return errors.New("only blocked deliveries can be resolved")
	}
	s, e := openObservationState(b.root)
	if e != nil {
		return e
	}
	t, e := s.Task(d.Task)
	s.DB.Close()
	if e != nil {
		return e
	}
	if t.Phase != "active" || t.Conversation == "" {
		return errors.New("first reconcile the controller and recover the original turn into active state; resolve never replays prompts")
	}
	var events struct {
		Job string `json:"jobId"`
	}
	r := Runner{State: &State{Namespace: s.Namespace}, Client: b.client}
	if _, e = r.owned(t); e != nil {
		return e
	}
	if e = b.client.Call(&events, "agent", "events", t.Box, "-c", t.Conversation); e != nil || events.Job == "" {
		return errors.New("cannot verify current native job")
	}
	_, e = b.db.Exec("UPDATE deliveries SET status='running',job=? WHERE id=?", events.Job, id)
	return e
}
