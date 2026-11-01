package main

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Runner struct {
	State   *State
	Client  Client
	Secrets Secrets
}

func (r *Runner) owned(t Task) (Box, error) {
	b := Box{}
	if e := r.Client.Call(&b, "get", t.Box); e != nil {
		return b, e
	}
	owned := b.Metadata["controller"] == r.State.Namespace || (t.Adopted == 1 && b.Metadata["purpose"] == "official-auth-probe")
	if b.ID != t.Box || b.Org != t.Org || b.Metadata["ticket"] != t.Ticket || !owned || b.Vault {
		return b, errors.New("VM ownership or credential isolation mismatch")
	}
	return b, nil
}
func (r *Runner) Create(ticket string, c Config, bootstrap bool) (Task, error) {
	if e := validID(ticket); e != nil {
		return Task{}, e
	}
	if e := c.Validate(); e != nil {
		return Task{}, e
	}
	if c.User == "" || c.Org == "" {
		return Task{}, errors.New("configure user and org")
	}
	var exists int
	if e := r.State.DB.QueryRow("SELECT count(*) FROM tasks WHERE ticket=?", ticket).Scan(&exists); e != nil {
		return Task{}, e
	}
	if exists > 0 {
		t, e := r.State.Task(ticket)
		if e != nil {
			return t, e
		}
		if t.User != c.User || t.Org != c.Org || t.Provider != c.Provider {
			return t, errors.New("task already belongs to another identity or provider")
		}
		if t.Phase == "creating" || t.Phase == "created" {
			return t, errors.New("creation requires reconciliation")
		}
		return t, nil
	}
	v, e := r.State.Version(c.User)
	if e != nil {
		return Task{}, e
	}
	if c.Provider == "codex" && v == "" && !bootstrap {
		return Task{}, errors.New("bootstrap dedicated Codex login first")
	}
	t := Task{Ticket: ticket, User: c.User, Provider: c.Provider, Org: c.Org, Name: strings.ToLower(ticket) + "-" + r.State.Namespace[:8], Phase: "creating"}
	if len(t.Name) > 63 {
		return t, errors.New("task ID too long for VM name")
	}
	if e = r.State.Save(t); e != nil {
		return t, e
	}
	selected := strings.Join(c.Secrets, ",")
	if selected == "" {
		selected = "none"
	}
	args := []string{"create", "--name", t.Name, "--org", t.Org, "--no-vault", "--secrets", selected, "--vcpus", strconv.Itoa(c.VCPUs), "--mem-mib", strconv.Itoa(c.Memory), "--auto-suspend", "10m", "--max-run-minutes", "60", "--fail-fast", "--meta", "ticket=" + ticket, "--meta", "controller=" + r.State.Namespace}
	if c.Snapshot != "" {
		args = append(args, "--snapshot", c.Snapshot)
	}
	var b Box
	if e = r.Client.Call(&b, args...); e != nil {
		return t, e
	}
	if b.ID == "" || b.Org != t.Org || b.Metadata["ticket"] != ticket || b.Metadata["controller"] != r.State.Namespace || b.Vault {
		return t, errors.New("create response missing ownership; reconcile before retrying")
	}
	t.Box = b.ID
	t.Phase = "created"
	if e = r.State.Save(t); e != nil {
		return t, e
	}
	if e = r.Client.Call(nil, "suspend", t.Box); e != nil {
		return t, e
	}
	t.Phase = "paused"
	e = r.State.Save(t)
	return t, e
}
func (r *Runner) Reconcile(ticket string) (Task, error) {
	t, e := r.State.Task(ticket)
	if e != nil {
		return t, e
	}
	if t.Phase == "created" {
		if _, e = r.owned(t); e != nil {
			return t, e
		}
	} else if t.Phase == "creating" {
		var boxes []Box
		if e = r.Client.Call(&boxes, "ls"); e != nil {
			return t, e
		}
		matches := []Box{}
		for _, b := range boxes {
			if b.Name == t.Name && b.Org == t.Org && b.Metadata["controller"] == r.State.Namespace && b.Metadata["ticket"] == ticket {
				matches = append(matches, b)
			}
		}
		if len(matches) != 1 || matches[0].Vault || (matches[0].State != "running" && matches[0].State != "suspended") {
			return t, errors.New("cannot reconcile exactly one running or suspended isolated owned VM")
		}
		t.Box = matches[0].ID
		// Persist the discovered VM before any further remote mutation.
		t.Phase = "created"
		if e = r.State.Save(t); e != nil {
			return t, e
		}
	} else {
		return t, nil
	}
	b, e := r.owned(t)
	if e != nil {
		return t, e
	}
	if b.State != "suspended" {
		if b.State != "running" {
			return t, errors.New("VM not ready for reconciliation")
		}
		if e = r.Client.Call(nil, "suspend", t.Box); e != nil {
			return t, e
		}
	}
	t.Phase = "paused"
	return t, r.State.Save(t)
}
func (r *Runner) Bootstrap(c Config) (Task, error) {
	if e := c.Validate(); e != nil {
		return Task{}, e
	}
	if c.Provider != "codex" {
		return Task{}, errors.New("bootstrap is Codex-only; use native Claude login in its own VM")
	}
	v, e := r.State.Version(c.User)
	if e != nil {
		return Task{}, e
	}
	if v != "" {
		return Task{}, errors.New("profile already has saved auth; refusing replacement")
	}
	// Auth bootstrap never inherits repo Secrets or a user-supplied snapshot.
	c.Secrets = nil
	c.Snapshot = ""
	t, e := r.Create("AUTH-"+c.User, c, true)
	if e != nil {
		return t, e
	}
	if e = r.State.Acquire(t.User, t.Ticket); e != nil {
		return t, e
	}
	b, e := r.owned(t)
	if e != nil {
		return t, e
	}
	if b.State == "suspended" {
		if e = r.Client.Call(nil, "resume", t.Box, "--no-vault"); e != nil {
			return t, e
		}
	}
	if _, e = r.Client.Shell(t.Box, "umask 077; mkdir -p /data/home/.codex; printf 'cli_auth_credentials_store = \"file\"\\n' > /data/home/.codex/config.toml"); e != nil {
		return t, e
	}
	t.Phase = "active"
	return t, r.State.Save(t)
}
func (r *Runner) Activate(ticket string) (Task, error) {
	t, e := r.State.Task(ticket)
	if e != nil {
		return t, e
	}
	if t.Phase != "paused" && t.Phase != "restoring" && t.Phase != "active" {
		return t, errors.New("task needs recovery before execution")
	}
	b, e := r.owned(t)
	if e != nil {
		return t, e
	}
	if t.Provider == "codex" {
		if e = r.State.Acquire(t.User, t.Ticket); e != nil {
			return t, e
		}
	}
	if t.Phase == "active" {
		if b.State == "suspended" {
			e = r.Client.Call(nil, "resume", t.Box, "--no-vault")
		} else if b.State != "running" {
			e = errors.New("active VM is not running; inspect before restarting")
		}
		return t, e
	}
	t.Phase = "restoring"
	if e = r.State.Save(t); e != nil {
		return t, e
	}
	if b.State == "suspended" {
		e = r.Client.Call(nil, "resume", t.Box, "--no-vault")
	} else if b.State == "stopped" {
		e = r.Client.Call(nil, "start", t.Box, "--no-vault")
	} else if b.State != "running" {
		e = errors.New("VM not ready")
	}
	if e != nil {
		return t, e
	}
	if t.Provider == "codex" {
		v, e := r.State.Version(t.User)
		if e != nil {
			return t, e
		}
		raw, e := r.Secrets.Get(t.User, v)
		if e != nil {
			return t, e
		}
		if _, e = r.Client.Credentials(t.Box, raw); e != nil {
			return t, e
		}
		t.Version = v
	}
	t.Phase = "active"
	return t, r.State.Save(t)
}
func (r *Runner) conversations(box string) ([]Conversation, error) {
	var v struct {
		Conversations []Conversation `json:"conversations"`
	}
	e := r.Client.Call(&v, "agent", "ls", box)
	if e == nil && v.Conversations == nil {
		e = errors.New("missing conversation list; refusing to assume idle")
	}
	return v.Conversations, e
}
func (r *Runner) Run(ticket, prompt, cwd, model string) (StartResult, error) {
	var result StartResult
	if strings.TrimSpace(prompt) == "" || len(prompt) > 256*1024 {
		return result, errors.New("task text must contain 1 to 262144 bytes")
	}
	t, e := r.Activate(ticket)
	if e != nil {
		return result, e
	}
	check := `set -e; set +u; test "${CODEX_HOME:-/data/home/.codex}" = /data/home/.codex; test "${CLAUDE_CONFIG_DIR:-/data/home/.claude}" = /data/home/.claude; for name in OPENAI_API_KEY CODEX_API_KEY CODEX_ACCESS_TOKEN OPENAI_BASE_URL ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL CLAUDE_CODE_OAUTH_TOKEN CLAUDE_CODE_USE_BEDROCK CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_FOUNDRY; do test -z "${!name}" || exit 1; done`
	if _, e = r.Client.Shell(t.Box, check); e != nil {
		return result, e
	}
	authCommand := "codex login status"
	if t.Provider == "claude" {
		authCommand = "claude auth status"
	}
	if _, e = r.Client.Shell(t.Box, authCommand); e != nil {
		return result, e
	}
	convs, e := r.conversations(t.Box)
	if e != nil {
		return result, e
	}
	for _, v := range convs {
		if !idleConversation(v.Status) {
			return result, errors.New("agent already running; follow existing conversation")
		}
	}
	args := []string{"agent", "prompt", t.Box}
	if t.Conversation != "" {
		args = append(args, "--resume", t.Conversation)
	} else {
		args = append(args, "--provider", t.Provider, "--cwd", cwd)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	t.Phase = "starting-turn"
	if e = r.State.Save(t); e != nil {
		return result, e
	}
	if e = r.Client.Prompt(&result, prompt, args...); e != nil {
		return result, e
	}
	if result.ID == "" {
		return result, errors.New("agent start response missing conversation; reconcile")
	}
	t.Conversation = result.ID
	t.Phase = "active"
	return result, r.State.Save(t)
}
func (r *Runner) RecoverTurn(ticket, conv string) (Task, error) {
	t, e := r.State.Task(ticket)
	if e != nil {
		return t, e
	}
	if _, e = r.owned(t); e != nil {
		return t, e
	}
	if t.Phase != "starting-turn" {
		return t, errors.New("task has no uncertain turn")
	}
	vs, e := r.conversations(t.Box)
	if e != nil {
		return t, e
	}
	for _, v := range vs {
		if v.ID == conv && v.Provider == t.Provider {
			t.Conversation = conv
			t.Phase = "active"
			return t, r.State.Save(t)
		}
	}
	return t, errors.New("select a verified existing conversation")
}
func (r *Runner) Prepare(ticket, repo, base string) (Task, error) {
	c := Config{Repo: repo, Base: base, Provider: "codex", VCPUs: 2, Memory: 4096}
	if !repoPattern.MatchString(repo) {
		return Task{}, errors.New("repo must be OWNER/REPO")
	}
	if e := c.Validate(); e != nil {
		return Task{}, e
	}
	t, e := r.Activate(ticket)
	if e != nil {
		return t, e
	}
	if t.Repo != "" {
		if t.Repo != repo || t.Base != base {
			return t, errors.New("task already owns another repo or base")
		}
		return t, nil
	}
	t.Repo = repo
	t.Base = base
	t.Branch = strings.ToLower(ticket) + "-agent"
	t.Phase = "preparing"
	if e = r.State.Save(t); e != nil {
		return t, e
	}
	cmd := "set -eu; test ! -e /data/home/workspace; if [ -n \"${GH_TOKEN:-}\" ]; then gh auth setup-git; fi; git clone --single-branch --branch " + quote(base) + " " + quote("https://github.com/"+repo+".git") + " /data/home/workspace; cd /data/home/workspace; git switch -c " + quote(t.Branch)
	if _, e = r.Client.Shell(t.Box, cmd); e != nil {
		return t, e
	}
	t.Phase = "active"
	return t, r.State.Save(t)
}
func (r *Runner) RecoverPrepare(ticket string) (Task, error) {
	t, e := r.State.Task(ticket)
	if e != nil {
		return t, e
	}
	if _, e = r.owned(t); e != nil {
		return t, e
	}
	if t.Phase != "preparing" || t.Repo == "" || t.Branch == "" {
		return t, errors.New("task is not awaiting preparation recovery")
	}
	cmd := "set -eu; cd /data/home/workspace; test \"$(git remote get-url origin)\" = " + quote("https://github.com/"+t.Repo+".git") + "; test \"$(git branch --show-current)\" = " + quote(t.Branch) + "; git rev-parse --verify HEAD"
	if _, e = r.Client.Shell(t.Box, cmd); e != nil {
		return t, e
	}
	t.Phase = "active"
	return t, r.State.Save(t)
}

var codingProcess = regexp.MustCompile(`(^|[ /])(codex|claude|claude-code)([ /.]|$)`)

func idleConversation(status string) bool {
	return status == "idle" || status == "interrupted" || status == "lost" || strings.HasPrefix(status, "failed(")
}

func (r *Runner) Checkpoint(ticket string) (Task, error) {
	t, e := r.State.Task(ticket)
	if e != nil {
		return t, e
	}
	b, e := r.owned(t)
	if e != nil {
		return t, e
	}
	if t.Phase == "paused" {
		return t, nil
	}
	if t.Phase != "active" && t.Phase != "auth-saved" && t.Phase != "credentials-removed" {
		return t, errors.New("uncertain operation needs reconciliation before checkpoint")
	}
	if t.Provider == "codex" {
		if e = r.State.Acquire(t.User, ticket); e != nil {
			return t, e
		}
	}
	if t.Phase != "credentials-removed" {
		// Paused process listings are cached. Resume before checking live work
		// or removing auth after a previously completed save.
		if b.State == "suspended" {
			if e = r.Client.Call(nil, "resume", t.Box, "--no-vault"); e != nil {
				return t, e
			}
			b.State = "running"
		} else if b.State != "running" {
			return t, errors.New("checkpoint needs a running or suspended VM; preserve lease and inspect")
		}
	}
	if t.Phase == "active" {
		vs, e := r.conversations(t.Box)
		if e != nil {
			return t, e
		}
		for _, v := range vs {
			if !idleConversation(v.Status) {
				return t, errors.New("agent still running; preserve lease")
			}
		}
		var jobs struct {
			Jobs []struct {
				Status string `json:"status"`
			} `json:"jobs"`
		}
		if e = r.Client.Call(&jobs, "jobs", "ls", t.Box); e != nil {
			return t, e
		}
		if jobs.Jobs == nil {
			return t, errors.New("missing job list; preserve lease")
		}
		for _, j := range jobs.Jobs {
			if j.Status != "exited" && j.Status != "lost" {
				return t, errors.New("job still running; preserve lease")
			}
		}
		var processes []Process
		if e = r.Client.Call(&processes, "ps", t.Box); e != nil {
			return t, e
		}
		if processes == nil {
			return t, errors.New("missing process list; preserve lease")
		}
		for _, p := range processes {
			if p.Command == "" || codingProcess.MatchString(p.Command) {
				return t, errors.New("coding CLI still running; preserve lease")
			}
		}
		if t.Provider == "codex" {
			v, e := r.State.Version(t.User)
			if e != nil {
				return t, e
			}
			if v != t.Version {
				return t, errors.New("stale credential version; refusing overwrite")
			}
			raw, e := r.Client.Credentials(t.Box, nil)
			if e != nil {
				return t, e
			}
			version := randomID()
			if e = r.Secrets.Put(t.User, version, raw); e != nil {
				return t, e
			}
			saved, e := r.Secrets.Get(t.User, version)
			if e != nil || !bytes.Equal(saved, raw) {
				return t, errors.New("saved login read-back failed; preserve guest auth and lease")
			}
			if e = r.State.CheckpointVersion(&t, version); e != nil {
				return t, e
			}
		} else {
			t.Phase = "auth-saved"
			if e = r.State.Save(t); e != nil {
				return t, e
			}
		}
	}
	if t.Phase == "auth-saved" {
		if t.Provider == "codex" {
			// Recovery may run after access to the OS store changed.
			if _, e = r.Secrets.Get(t.User, t.Version); e != nil {
				return t, errors.New("saved login unavailable; preserve guest auth and lease")
			}
			if _, e = r.Client.Shell(t.Box, "rm -f /data/home/.codex/auth.json"); e != nil {
				return t, e
			}
		}
		t.Phase = "credentials-removed"
		if e = r.State.Save(t); e != nil {
			return t, e
		}
	}
	if b.State != "suspended" {
		if b.State != "running" {
			return t, errors.New("VM not ready to finish checkpoint; preserve lease")
		}
		if e = r.Client.Call(nil, "suspend", t.Box); e != nil {
			return t, e
		}
	}
	return t, r.State.Release(&t)
}
func (r *Runner) Status(ticket string) (map[string]any, error) {
	t, e := r.State.Task(ticket)
	if e != nil {
		return nil, e
	}
	b, e := r.owned(t)
	if e != nil {
		return nil, e
	}
	return map[string]any{"task": t, "vm_state": b.State}, nil
}
func requireArg(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("expected one task ID")
	}
	return args[0], validID(args[0])
}
