package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Box struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Org      string            `json:"orgId"`
	State    string            `json:"state"`
	Vault    bool              `json:"vault"`
	Metadata map[string]string `json:"metadata"`
}
type Conversation struct {
	ID       string `json:"conversationId"`
	Provider string `json:"provider"`
	Status   string `json:"status"`
}
type Process struct {
	Command string `json:"command"`
}
type StartResult struct {
	ID   string `json:"conversationId"`
	Job  string `json:"jobId"`
	Turn int    `json:"turn"`
}
type Client interface {
	Call(any, ...string) error
	Shell(string, string) (string, error)
	Credentials(string, []byte) ([]byte, error)
}

type Instabox struct{}

func (i Instabox) Call(out any, args ...string) error {
	argv := append([]string{}, args...)
	pos := len(argv)
	for n, s := range argv {
		if s == "--" {
			pos = n
			break
		}
	}
	argv = append(argv, "")
	copy(argv[pos+1:], argv[pos:])
	argv[pos] = "--json"
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "instabox", argv...)
	raw, e := cmd.Output()
	if ctx.Err() != nil {
		return errors.New("Instabox outcome uncertain; reconcile before retrying")
	}
	if e != nil {
		return errors.New("Instabox operation failed; inspect task state privately")
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errors.New("unexpected Instabox response; reconcile before retrying")
	}
	return nil
}
func (i Instabox) Shell(box, command string) (string, error) {
	var r struct {
		Code   int    `json:"exitCode"`
		Stdout string `json:"stdout"`
	}
	if e := i.Call(&r, "exec", box, "--", "bash", "-lc", command); e != nil {
		return "", e
	}
	if r.Code != 0 {
		return "", errors.New("remote prerequisite failed; inspect VM privately")
	}
	return r.Stdout, nil
}
func (i Instabox) copy(source, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if e := exec.CommandContext(ctx, "instabox", "cp", source, target).Run(); e != nil {
		return errors.New("credential transfer failed; values were not logged")
	}
	return nil
}
func (i Instabox) Credentials(box string, value []byte) ([]byte, error) {
	dir, e := os.MkdirTemp("", "ib-ticket-auth-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "auth.json")
	if e = os.WriteFile(p, nil, 0600); e != nil {
		return nil, e
	}
	if value == nil {
		if e = i.copy(box+":/data/home/.codex/auth.json", p); e != nil {
			return nil, e
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			return nil, errors.New("cannot read transferred auth")
		}
		return validateAuth(raw)
	}
	if _, e = validateAuth(value); e != nil {
		return nil, e
	}
	if e = os.WriteFile(p, value, 0600); e != nil {
		return nil, e
	}
	if _, e = i.Shell(box, "umask 077; mkdir -p /data/home/.codex; chmod 700 /data/home/.codex"); e != nil {
		return nil, e
	}
	if e = i.copy(p, box+":/data/home/.codex/auth.json"); e != nil {
		return nil, e
	}
	_, e = i.Shell(box, "chmod 600 /data/home/.codex/auth.json; printf 'cli_auth_credentials_store = \"file\"\\n' > /data/home/.codex/config.toml")
	return nil, e
}
