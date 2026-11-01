package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLinear(t *testing.T) (*linearBridge, *Runner, *observationClient, *[]string) {
	t.Helper()
	o, c := testObserver(t)
	r := &Runner{State: o.state, Client: c}
	db, e := openLinearDB(r.State.Root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	token := filepath.Join(t.TempDir(), "oauth-token")
	os.WriteFile(token, []byte("synthetic-oauth-token"), 0600)
	calls := []string{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer synthetic-oauth-token" {
			t.Error("missing OAuth authorization")
		}
		var body struct {
			Query     string                     `json:"query"`
			Variables map[string]json.RawMessage `json:"variables"`
		}
		json.NewDecoder(req.Body).Decode(&body)
		if strings.Contains(body.Query, "agentActivityCreate") {
			fmt.Fprint(w, `{"data":{"agentActivityCreate":{"success":true}}}`)
		} else {
			fmt.Fprint(w, `{"data":{"agentSessionUpdate":{"success":true}}}`)
		}
	}))
	t.Cleanup(api.Close)
	b := &linearBridge{db: db, root: r.State.Root, client: c, secret: []byte("synthetic-signing-secret-123456"), settings: linearSettings{Workspace: "workspace", AppUser: "app", Teams: []string{"team"}, Users: []string{"human"}, Dashboard: "https://tasks.example.com", TokenFile: token}, http: api.Client(), endpoint: api.URL}
	b.dispatch = func(args ...string) (any, error) { calls = append(calls, args[0]); return nil, nil }
	return b, r, c, &calls
}
func linearPayload(action string) map[string]any {
	return map[string]any{"type": "AgentSessionEvent", "action": action, "organizationId": "workspace", "appUserId": "app", "webhookTimestamp": time.Now().UnixMilli(), "promptContext": "Fix the example; do not publish.", "agentSession": map[string]any{"id": "session", "creatorId": "human", "issue": map[string]any{"id": "issue", "teamId": "team"}}, "agentActivity": map[string]any{"id": "activity", "agentSessionId": "session", "userId": "human", "content": map[string]string{"type": "prompt", "body": "Please add a test."}}}
}
func sendLinear(b *linearBridge, payload map[string]any, valid bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(payload)
	r := httptest.NewRequest("POST", "/webhooks/linear", strings.NewReader(string(raw)))
	mac := hmac.New(sha256.New, b.secret)
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))
	if !valid {
		sig = "bad"
	}
	r.Header.Set("Linear-Signature", sig)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	return w
}
func TestLinearSignedInboxAndDedupe(t *testing.T) {
	b, _, _, calls := testLinear(t)
	for n := 0; n < 2; n++ {
		if w := sendLinear(b, linearPayload("created"), true); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := sendLinear(b, linearPayload("prompted"), true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	var count int
	b.db.QueryRow("SELECT count(*) FROM deliveries").Scan(&count)
	if count != 2 || len(*calls) != 0 {
		t.Fatalf("duplicate dispatch or lost inbox: %d %v", count, *calls)
	}
	d, e := b.delivery("SELECT " + deliveryColumns + " FROM deliveries ORDER BY seq LIMIT 1")
	if e != nil || d.Status != "queued" || !strings.HasPrefix(d.Task, "LIN-") {
		t.Fatal(d, e)
	}
	// Receipt survives reopening the service database.
	reopened, e := openLinearDB(b.root)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if e = reopened.QueryRow("SELECT count(*) FROM deliveries").Scan(&count); e != nil || count != 2 {
		t.Fatal(e, count)
	}
}
func TestLinearRejectsInvalidSignatureScopeAndReplay(t *testing.T) {
	for _, kind := range []string{"signature", "stale", "workspace", "app", "team", "user", "self", "activity-session", "not-prompt"} {
		t.Run(kind, func(t *testing.T) {
			b, _, _, _ := testLinear(t)
			p := linearPayload("prompted")
			valid := true
			switch kind {
			case "signature":
				valid = false
			case "stale":
				p["webhookTimestamp"] = time.Now().Add(-2 * time.Minute).UnixMilli()
			case "workspace":
				p["organizationId"] = "foreign"
			case "app":
				p["appUserId"] = "foreign"
			case "team":
				p["agentSession"].(map[string]any)["issue"].(map[string]any)["teamId"] = "foreign"
			case "user":
				p["agentActivity"].(map[string]any)["userId"] = "foreign"
			case "self":
				p["agentActivity"].(map[string]any)["userId"] = "app"
			case "activity-session":
				p["agentActivity"].(map[string]any)["agentSessionId"] = "foreign"
			case "not-prompt":
				p["agentActivity"].(map[string]any)["content"] = map[string]string{"type": "response", "body": "self output"}
			}
			if w := sendLinear(b, p, valid); w.Code < 400 {
				t.Fatal("accepted", kind, w.Code)
			}
			var n int
			b.db.QueryRow("SELECT count(*) FROM deliveries").Scan(&n)
			if n != 0 {
				t.Fatal("rejected event persisted")
			}
		})
	}
}
func enqueueLinear(t *testing.T, b *linearBridge, id string) {
	t.Helper()
	_, e := b.db.Exec("INSERT INTO deliveries(id,session,task,prompt,status,ack) VALUES(?,'session','TASK-1','Fix example','queued',1)", id)
	if e != nil {
		t.Fatal(e)
	}
}
func readDelivery(t *testing.T, b *linearBridge, id string) linearDelivery {
	t.Helper()
	d, e := b.delivery("SELECT " + deliveryColumns + " FROM deliveries WHERE id='" + id + "'")
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestLinearRunsFollowupInSameTaskAndWaitsForExactJob(t *testing.T) {
	b, r, c, calls := testLinear(t)
	// Use a separate writable state, as production dispatch does.
	writable, e := openState(b.root)
	if e == nil {
		writable.Close()
		t.Fatal("test writer fixture already holds normal controller lock")
	}
	// The seed fixture holds the writer; save through its existing connection via a new SQL handle is safe for this synthetic test.
	db, e := openLinearTestWriter(b.root)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	b.dispatch = func(args ...string) (any, error) {
		*calls = append(*calls, args[0])
		if args[0] == "run" {
			if _, e := os.ReadFile(args[len(args)-1]); e != nil {
				t.Error(e)
			}
			return StartResult{ID: "conv-one", Job: "job-one"}, nil
		}
		if args[0] == "checkpoint" {
			_, e := db.Exec("UPDATE tasks SET phase='paused' WHERE ticket='TASK-1'")
			return nil, e
		}
		return nil, nil
	}
	enqueueLinear(t, b, "first")
	b.start(readDelivery(t, b, "first"))
	first := readDelivery(t, b, "first")
	if first.Status != "running" || first.Job != "job-one" {
		t.Fatal(first)
	}
	// observationClient's response has no job ID, so stale output cannot finish the new turn.
	b.monitor(first)
	if readDelivery(t, b, "first").Status != "running" {
		t.Fatal("completed from a stale job")
	}
	b.client = &linearEventClient{observationClient: c, job: "job-one", status: "idle"}
	b.monitor(first)
	if d := readDelivery(t, b, "first"); d.Status != "done" || d.Prompt != "" || !strings.Contains(d.Reply, "new final answer") {
		t.Fatal(d)
	}
	task, _ := r.State.Task("TASK-1")
	if task.Box != "probe" || task.Conversation != "conv-one" || task.Phase != "paused" {
		t.Fatal(task)
	}
	enqueueLinear(t, b, "second")
	b.start(readDelivery(t, b, "second"))
	if d := readDelivery(t, b, "second"); d.Status != "running" {
		t.Fatal(d)
	}
	if strings.Join(*calls, ",") != "create,run,checkpoint,create,run" {
		t.Fatal(*calls)
	}
}

type linearEventClient struct {
	*observationClient
	job, status string
	eventsFail  bool
}

func (c *linearEventClient) Call(out any, args ...string) error {
	if args[0] == "agent" && args[1] == "ls" {
		raw, _ := json.Marshal(map[string]any{"conversations": []any{map[string]any{"conversationId": "conv-one", "provider": "codex", "jobId": c.job, "status": c.status, "exitCode": 0}}})
		return json.Unmarshal(raw, out)
	}
	if args[0] == "agent" && args[1] == "events" {
		if c.eventsFail {
			return errors.New("transcript output limit")
		}
		raw, _ := json.Marshal(map[string]any{"jobId": c.job, "status": c.status, "exitCode": 0, "events": []any{map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": "new final answer"}}}})
		return json.Unmarshal(raw, out)
	}
	return c.observationClient.Call(out, args...)
}
func TestLinearUncertainDispatchNeverReplaysOrRunsFollowup(t *testing.T) {
	b, _, _, calls := testLinear(t)
	enqueueLinear(t, b, "first")
	enqueueLinear(t, b, "second")
	b.dispatch = func(args ...string) (any, error) {
		*calls = append(*calls, args[0])
		return nil, errors.New("sensitive provider output")
	}
	b.step()
	b.step()
	b.step()
	if strings.Join(*calls, ",") != "create" {
		t.Fatal("uncertain operation replayed", *calls)
	}
	if d := readDelivery(t, b, "first"); d.Status != "attention" || strings.Contains(d.Reply, "sensitive") {
		t.Fatal(d)
	}
	if d := readDelivery(t, b, "second"); d.Status != "queued" {
		t.Fatal("followup bypassed failed work", d)
	}
}
func TestLinearCheckpointFailurePreservesReplyAndBlocksNextTurn(t *testing.T) {
	b, _, c, calls := testLinear(t)
	enqueueLinear(t, b, "first")
	b.db.Exec("UPDATE deliveries SET status='running',job='job-one'")
	b.client = &linearEventClient{observationClient: c, job: "job-one", status: "idle"}
	b.dispatch = func(args ...string) (any, error) {
		*calls = append(*calls, args[0])
		return nil, errors.New("store inaccessible")
	}
	b.monitor(readDelivery(t, b, "first"))
	d := readDelivery(t, b, "first")
	if d.Status != "blocked" || !strings.Contains(d.Reply, "new final answer") {
		t.Fatal(d)
	}
}
func TestLinearResponseExcludesPreviousTurnAndReasoning(t *testing.T) {
	events := []json.RawMessage{json.RawMessage(`{"item":{"type":"agent_message","text":"old answer"}}`), json.RawMessage(`{"type":"instabox.turn"}`), json.RawMessage(`{"item":{"type":"reasoning","text":"private reasoning"}}`)}
	text := agentReply(events)
	if strings.Contains(text, "old answer") || strings.Contains(text, "private reasoning") {
		t.Fatal(text)
	}
}

func openLinearTestWriter(root string) (*sql.DB, error) {
	return sql.Open("sqlite", filepath.Join(root, "state.db"))
}

func TestLinearAPIFailureDoesNotPreventCheckpoint(t *testing.T) {
	b, _, c, calls := testLinear(t)
	enqueueLinear(t, b, "first")
	enqueueLinear(t, b, "second")
	b.db.Exec("UPDATE deliveries SET status='running',job='job-one',ack=1 WHERE id='first'")
	b.db.Exec("UPDATE deliveries SET ack=0 WHERE id='second'")
	b.client = &linearEventClient{observationClient: c, job: "job-one", status: "idle"}
	// All outbound Linear calls fail, including receipt/running updates.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
	defer server.Close()
	b.endpoint = server.URL
	b.http = server.Client()
	b.streams = newTurnStreams(context.Background())
	defer b.streams.close()
	b.step()
	select {
	case result := <-b.streams.results:
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("completion did not arrive")
	}
	if strings.Join(*calls, ",") != "checkpoint" {
		t.Fatal("Linear outage prevented automatic checkpoint", *calls)
	}
	if d := readDelivery(t, b, "first"); d.Status != "finishing" {
		t.Fatal("must retain response for later reporting", d)
	}
}

func TestLinearActivityTimeoutChecksStableIDBeforeRetry(t *testing.T) {
	b, _, _, _ := testLinear(t)
	delivery := linearDelivery{ID: "prompt:activity", Session: "session"}
	var sentID string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string                     `json:"query"`
			Variables map[string]json.RawMessage `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.Query, "agentActivityCreate") {
			var input struct {
				ID      string `json:"id"`
				Session string `json:"agentSessionId"`
			}
			json.Unmarshal(request.Variables["input"], &input)
			sentID = input.ID
			if input.Session != "session" || input.ID != activityUUID(delivery.ID, "response") {
				t.Error("unstable outbound identity")
			}
			fmt.Fprint(w, `{"errors":[{"message":"already committed or uncertain"}]}`)
		} else {
			var id string
			json.Unmarshal(request.Variables["id"], &id)
			if id != sentID {
				t.Error("checked a different activity")
			}
			fmt.Fprint(w, `{"data":{"agentActivity":{"agentSession":{"id":"session"}}}}`)
		}
	}))
	defer api.Close()
	b.endpoint = api.URL
	b.http = api.Client()
	if e := b.activity(delivery, "response", "done"); e != nil {
		t.Fatal(e)
	}
	if sentID == "" {
		t.Fatal("missing activity")
	}
}

func (c *linearEventClient) Follow(ctx context.Context, box, conversation string) error { return nil }

func TestLinearTranscriptFailureDoesNotPreventConfirmedCheckpoint(t *testing.T) {
	b, _, c, calls := testLinear(t)
	enqueueLinear(t, b, "first")
	b.db.Exec("UPDATE deliveries SET status='running',job='job-one'")
	b.client = &linearEventClient{observationClient: c, job: "job-one", status: "idle", eventsFail: true}
	done, err := b.monitor(readDelivery(t, b, "first"))
	if err != nil || !done || strings.Join(*calls, ",") != "checkpoint" {
		t.Fatal(done, err, *calls)
	}
	d := readDelivery(t, b, "first")
	if d.Status != "done" || !strings.Contains(d.Reply, "details are unavailable") {
		t.Fatal(d)
	}
}
