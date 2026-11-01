package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

type taskView struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Phase        string `json:"phase"`
	Box          string `json:"box"`
	Conversation string `json:"conversation"`
	Repo         string `json:"repo"`
	Branch       string `json:"branch"`
	PR           string `json:"pr,omitempty"`
}

func viewTask(t Task) taskView {
	return taskView{t.Ticket, t.Provider, t.Phase, t.Box, t.Conversation, t.Repo, t.Branch, t.PR}
}

type observation struct {
	Task          taskView          `json:"task"`
	At            time.Time         `json:"at"`
	VM            string            `json:"vm"`
	Status        string            `json:"status"`
	ExitCode      *int              `json:"exitCode"`
	Events        []json.RawMessage `json:"events"`
	EventsOmitted int               `json:"eventsOmitted"`
	Git           string            `json:"git"`
	GitTruncated  bool              `json:"gitTruncated"`
	Notes         []string          `json:"notes"`
}
type observer struct {
	state    *State
	client   Client
	password [32]byte
	mu       sync.Mutex
	cache    map[string]observation
}

// A separate read-only connection never takes the dispatcher's process lock.
func openObservationState(root string) (*State, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	for _, p := range []string{root, filepath.Join(root, "state.db")} {
		info, err := os.Lstat(p)
		if err != nil {
			return nil, errors.New("initialize controller state before serving")
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("observation state must be private and not symlinked")
		}
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(root, "state.db")}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(2000)")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &State{DB: db, Root: root}
	if e = db.QueryRow("SELECT value FROM metadata WHERE key='namespace'").Scan(&s.Namespace); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func serveDashboard(root string, args []string) error {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := f.String("listen", "127.0.0.1:8787", "loopback listener behind an HTTPS proxy")
	passwordFile := f.String("password-file", "", "private file containing at least 24 random characters")
	if e := f.Parse(args); e != nil {
		return e
	}
	if len(f.Args()) != 0 {
		return errors.New("unexpected serve arguments")
	}
	host, _, e := net.SplitHostPort(*listen)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("serve requires a loopback IP; use an HTTPS reverse proxy for remote access")
	}
	p := expandHome(*passwordFile)
	info, e := os.Lstat(p)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return errors.New("provide a private regular password file (mode 0600)")
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return errors.New("cannot read dashboard password file")
	}
	password := strings.TrimSpace(string(raw))
	if len(password) < 24 {
		return errors.New("dashboard password requires at least 24 random characters")
	}
	s, e := openObservationState(expandHome(root))
	if e != nil {
		return e
	}
	defer s.DB.Close()
	o := &observer{state: s, client: Instabox{Timeout: 20 * time.Second, MaxOutput: 8 << 20}, password: sha256.Sum256([]byte(password)), cache: make(map[string]observation)}
	server := &http.Server{Addr: *listen, Handler: o, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	fmt.Fprintf(os.Stderr, "Dashboard: http://%s (username: operator; remote access requires HTTPS proxy)\n", *listen)
	return server.ListenAndServe()
}
func (o *observer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	user, password, ok := r.BasicAuth()
	digest := sha256.Sum256([]byte(password))
	if !ok || user != "operator" || subtle.ConstantTimeCompare(digest[:], o.password[:]) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="Instabox tasks", charset="UTF-8"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Read only", 405)
		return
	}
	switch r.URL.Path {
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, dashboardHTML)
	case "/app.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		fmt.Fprint(w, dashboardJS)
	case "/style.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		fmt.Fprint(w, dashboardCSS)
	case "/api/tasks":
		rows, e := o.state.DB.Query("SELECT ticket FROM tasks ORDER BY ticket LIMIT 500")
		if e != nil {
			http.Error(w, "State unavailable", 503)
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			http.Error(w, "State unavailable", 503)
			return
		}
		tasks := []taskView{}
		for _, id := range ids {
			t, e := o.state.Task(id)
			if e != nil {
				http.Error(w, "State unavailable", 503)
				return
			}
			tasks = append(tasks, viewTask(t))
		}
		writeObservationJSON(w, tasks)
	case "/api/task":
		id := r.URL.Query().Get("id")
		if validID(id) != nil {
			http.Error(w, "Invalid task", 400)
			return
		}
		t, e := o.state.Task(id)
		if e != nil {
			http.Error(w, "Task unavailable", 404)
			return
		}
		// One bounded collection at a time, with a short cache to avoid polling storms.
		if !o.mu.TryLock() {
			http.Error(w, "Refreshing another task; retry shortly", 503)
			return
		}
		defer o.mu.Unlock()
		v, ok := o.cache[id]
		if !ok || time.Since(v.At) > 15*time.Second || v.Task != viewTask(t) {
			v = o.collect(t)
			o.cache[id] = v
		}
		writeObservationJSON(w, v)
	default:
		http.NotFound(w, r)
	}
}
func writeObservationJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (o *observer) collect(t Task) observation {
	v := observation{Task: viewTask(t), At: time.Now().UTC(), Status: "unavailable", Events: []json.RawMessage{}, Notes: []string{}}
	var supervisionNote string
	if o.state.DB.QueryRow("SELECT note FROM turns WHERE ticket=? AND status='attention'", t.Ticket).Scan(&supervisionNote) == nil && supervisionNote != "" {
		v.Notes = append(v.Notes, supervisionNote)
	}
	if t.Box == "" {
		v.Notes = append(v.Notes, "VM creation has not been reconciled.")
		return v
	}
	r := Runner{State: o.state, Client: o.client}
	b, e := r.owned(t)
	if e != nil {
		v.Notes = append(v.Notes, "VM unavailable or ownership check failed. No guest data was read.")
		return v
	}
	v.VM = b.State
	if b.State != "running" {
		if saved, err := readSavedObservation(o.state.Root, t); err == nil {
			saved.Task = viewTask(t)
			saved.VM = b.State
			saved.Notes = append(saved.Notes, "Saved before checkpoint; this is not a live VM view.")
			return saved
		}
		v.Notes = append(v.Notes, "VM is not running. Observation will not resume it; live events and diff are unavailable.")
		return v
	}
	if t.Conversation != "" {
		var events struct {
			Status   string            `json:"status"`
			ExitCode *int              `json:"exitCode"`
			Events   []json.RawMessage `json:"events"`
		}
		if e = o.client.Call(&events, "agent", "events", t.Box, "-c", t.Conversation); e != nil {
			v.Notes = append(v.Notes, "Agent events unavailable (timeout, output limit or provider error).")
		} else {
			v.Status = events.Status
			v.ExitCode = events.ExitCode
			if len(events.Events) > 150 {
				v.EventsOmitted = len(events.Events) - 150
				events.Events = events.Events[len(events.Events)-150:]
			}
			// Bound each rendered event. Full command outputs remain in native events.
			for _, event := range events.Events {
				if len(event) > 32<<10 {
					event = json.RawMessage(`{"type":"display_limit","message":"Large event omitted. Inspect native agent events on the trusted host."}`)
				}
				v.Events = append(v.Events, event)
			}
		}
	}
	if t.Repo != "" && t.Branch != "" {
		// No browser-controlled paths or commands; disable external diff/textconv and
		// optional index writes. Only tracked diff and untracked names are displayed.
		command := "set -eu; export GIT_OPTIONAL_LOCKS=0; cd /data/home/workspace; test \"$(git remote get-url origin)\" = " + quote("https://github.com/"+t.Repo+".git") + "; test \"$(git branch --show-current)\" = " + quote(t.Branch) + "; git -c core.fsmonitor=false status --short; base=$(git merge-base HEAD " + quote("origin/"+t.Base) + "); git --no-pager -c core.fsmonitor=false diff --no-ext-diff --no-textconv \"$base\" -- ."
		var result struct {
			Code      *int   `json:"exitCode"`
			Stdout    string `json:"stdout"`
			Truncated bool   `json:"truncated"`
			TimedOut  bool   `json:"timedOut"`
		}
		err := o.client.Call(&result, "exec", t.Box, "--", "bash", "-lc", command)
		output := result.Stdout
		if err != nil || result.Code == nil || *result.Code != 0 || result.TimedOut {
			v.Notes = append(v.Notes, "Repository diff unavailable; checkout, branch or base could not be verified.")
		} else {
			if len(output) > 180000 {
				output = output[:180000]
				v.GitTruncated = true
			}
			v.GitTruncated = v.GitTruncated || result.Truncated
			v.Git = output
		}
	}
	return v
}

//go:embed dashboard.js
var dashboardJS string

//go:embed dashboard.css
var dashboardCSS string

func validPRURL(raw, repo string) bool {
	u, e := url.Parse(raw)
	return e == nil && repo != "" && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && regexp.MustCompile("^/"+regexp.QuoteMeta(repo)+"/pull/[1-9][0-9]*$").MatchString(u.Path)
}

func observationPath(root, task string) string {
	sum := sha256.Sum256([]byte(task))
	return filepath.Join(root, "observations", hex.EncodeToString(sum[:])+".json")
}
func saveObservation(root string, v observation) error {
	path := observationPath(root, v.Task.ID)
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("observation directory must be private and not symlinked")
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if e := encoder.Encode(v); e != nil {
		return e
	}
	raw := buffer.Bytes()
	if len(raw) > 8<<20 {
		return errors.New("observation exceeds storage limit")
	}
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func readSavedObservation(root string, t Task) (observation, error) {
	var v observation
	path := observationPath(root, t.Ticket)
	for n, p := range []string{filepath.Dir(path), path} {
		info, e := os.Lstat(p)
		if e != nil || (n == 0 && !info.IsDir()) || (n == 1 && !info.Mode().IsRegular()) || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || info.Size() > 8<<20 {
			return v, errors.New("saved observation unavailable")
		}
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return v, e
	}
	if json.Unmarshal(raw, &v) != nil || v.Task.ID != t.Ticket || v.Task.Box != t.Box || v.Task.Conversation != t.Conversation {
		return observation{}, errors.New("saved observation mismatch")
	}
	return v, nil
}
func (r *Runner) captureObservation(t Task) error {
	if t.Conversation == "" {
		return nil
	}
	client := r.Client
	if _, ok := client.(Instabox); ok {
		client = Instabox{Timeout: 20 * time.Second, MaxOutput: 8 << 20}
	}
	o := observer{state: r.State, client: client}
	if e := saveObservation(r.State.Root, o.collect(t)); e != nil {
		return errors.New("cannot save private task observation; preserve work and lease")
	}
	return nil
}
