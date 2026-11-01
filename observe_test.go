package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type observationClient struct {
	*fakeClient
	eventError bool
	diffError  bool
}

func (f *observationClient) Call(out any, args ...string) error {
	if args[0] == "agent" && args[1] == "events" {
		f.calls = append(f.calls, args)
		if f.eventError {
			return errors.New("private failure detail")
		}
		events := []map[string]any{}
		for n := 0; n < 155; n++ {
			events = append(events, map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "command": "go test ./...", "exit_code": 1, "aggregated_output": "<script>alert(1)</script> test failed"}})
		}
		raw, _ := json.Marshal(map[string]any{"status": "idle", "exitCode": 0, "events": events})
		return json.Unmarshal(raw, out)
	}
	if args[0] == "exec" {
		f.calls = append(f.calls, args)
		if f.diffError {
			return errors.New("private diff error")
		}
		raw := []byte(`{"exitCode":0,"stdout":" M app.go\n+changed","truncated":true}`)
		return json.Unmarshal(raw, out)
	}
	return f.fakeClient.Call(out, args...)
}
func testObserver(t *testing.T) (*observer, *observationClient) {
	t.Helper()
	r, f, _ := setup(t)
	seedTask(t, r, f)
	task, _ := r.State.Task("TASK-1")
	task.Repo = "example/project"
	task.Base = "main"
	task.Branch = "task/fix"
	r.State.Save(task)
	ro, e := openObservationState(r.State.Root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ro.DB.Close() })
	c := &observationClient{fakeClient: f}
	return &observer{state: ro, client: c, password: sha256.Sum256([]byte("test-only-password-123456789")), cache: map[string]observation{}}, c
}
func dashboardRequest(o *observer, path, method string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if auth {
		r.SetBasicAuth("operator", "test-only-password-123456789")
	}
	w := httptest.NewRecorder()
	o.ServeHTTP(w, r)
	return w
}
func TestDashboardAuthenticationAndReadOnly(t *testing.T) {
	o, _ := testObserver(t)
	for _, path := range []string{"/", "/app.js", "/style.css", "/api/tasks", "/api/task?id=TASK-1"} {
		if w := dashboardRequest(o, path, "GET", false); w.Code != 401 || strings.Contains(w.Body.String(), "TASK-1") {
			t.Fatalf("unauthorized %s: %d", path, w.Code)
		}
	}
	if w := dashboardRequest(o, "/api/task?id=TASK-1", "POST", true); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := dashboardRequest(o, "/api/tasks", "GET", true); w.Code != 200 || strings.Contains(w.Body.String(), "owner") || !strings.Contains(w.Body.String(), "TASK-1") {
		t.Fatal(w.Body.String())
	}
	if _, e := o.state.DB.Exec("DELETE FROM tasks"); e == nil {
		t.Fatal("observation connection was writable")
	}
	// Dispatch can acquire its normal process lock while the dashboard stays open.
	o.state.DB.Close()
}
func TestDashboardReadConnectionDoesNotHoldControllerLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, e := openState(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	ro, e := openObservationState(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer ro.DB.Close()
	writer, e := openState(dir)
	if e != nil {
		t.Fatalf("dashboard blocked dispatch: %v", e)
	}
	defer writer.Close()
	if e = writer.Save(Task{Ticket: "new", User: "u", Provider: "codex", Org: "o", Name: "n", Phase: "creating"}); e != nil {
		t.Fatal(e)
	}
	if _, e = ro.Task("new"); e != nil {
		t.Fatal(e)
	}
}
func TestDashboardObservationAndCache(t *testing.T) {
	o, c := testObserver(t)
	w := dashboardRequest(o, "/api/task?id=TASK-1", "GET", true)
	var v observation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatal(w.Body.String())
	}
	if len(v.Events) != 150 || v.EventsOmitted != 5 || v.ExitCode == nil || *v.ExitCode != 0 || !v.GitTruncated {
		t.Fatalf("bad observation: %+v", v)
	}
	if strings.Contains(w.Body.String(), "<script>") {
		t.Fatal("unsafe JSON encoding")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	n := len(c.calls)
	dashboardRequest(o, "/api/task?id=TASK-1", "GET", true)
	if len(c.calls) != n {
		t.Fatal("poll bypassed cache")
	}
	for _, call := range c.calls {
		if call[0] != "get" && call[0] != "agent" && call[0] != "exec" {
			t.Fatalf("mutation: %v", call)
		}
	}
}
func TestDashboardPausedOrForeignVMNeverReadsGuest(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused", true: "foreign"}[foreign], func(t *testing.T) {
			o, c := testObserver(t)
			b := c.boxes["probe"]
			if foreign {
				b.Metadata["controller"] = "other"
			} else {
				b.State = "suspended"
			}
			c.boxes["probe"] = b
			w := dashboardRequest(o, "/api/task?id=TASK-1", "GET", true)
			if w.Code != 200 || len(c.calls) != 1 || c.calls[0][0] != "get" || len(c.shells) != 0 {
				t.Fatalf("read or woke guest: %v %s", c.calls, w.Body.String())
			}
		})
	}
}
func TestDashboardPartialFailureAndLimits(t *testing.T) {
	o, c := testObserver(t)
	c.eventError = true
	w := dashboardRequest(o, "/api/task?id=TASK-1", "GET", true)
	if strings.Contains(w.Body.String(), "private failure detail") || !strings.Contains(w.Body.String(), "Agent events unavailable") || !strings.Contains(w.Body.String(), "changed") {
		t.Fatal(w.Body.String())
	}
	o.mu.Lock()
	w = dashboardRequest(o, "/api/task?id=TASK-1", "GET", true)
	o.mu.Unlock()
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	if w = dashboardRequest(o, "/api/task?id=../secret", "GET", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestDashboardServeRejectsUnsafeConfiguration(t *testing.T) {
	if e := serveDashboard(t.TempDir(), []string{"--listen", "0.0.0.0:8787"}); e == nil {
		t.Fatal("public bind allowed")
	}
	p := filepath.Join(t.TempDir(), "password")
	os.WriteFile(p, []byte("too-short"), 0600)
	if e := serveDashboard(t.TempDir(), []string{"--password-file", p}); e == nil {
		t.Fatal("weak password allowed")
	}
	os.Chmod(p, 0644)
	if e := serveDashboard(t.TempDir(), []string{"--password-file", p}); e == nil {
		t.Fatal("public password file allowed")
	}
}
func TestPRLinkIsRestrictedToPreparedRepository(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "https://github.com/other/repo/pull/1", "https://github.com@example.com/example/project/pull/1", "https://github.com/example/project/pull/1?token=secret"} {
		if validPRURL(raw, "example/project") {
			t.Fatal(raw)
		}
	}
	if !validPRURL("https://github.com/example/project/pull/12", "example/project") {
		t.Fatal("valid link rejected")
	}
}
func TestInstaboxReadOutputIsBounded(t *testing.T) {
	fakeInstabox(t, "printf '%01000d' 0")
	var result any
	if e := (Instabox{MaxOutput: 100}).Call(&result, "get", "box"); e == nil {
		t.Fatal("oversize output accepted")
	}
}
