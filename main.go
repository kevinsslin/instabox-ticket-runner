package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const Version = "0.1.0"

func help() {
	fmt.Println(`ib-ticket [--config PATH] [--state PATH] COMMAND

Commands:
  version                         Print controller version
  init-key                        Generate a private headless-store key (never overwrite)
  bootstrap                       Create dedicated official Codex login VM
  create --task ID                Create one task VM (configurable user/org/size)
  prepare ID                      Clone configured repository and create task branch
  run ID --prompt-file PATH       Start or resume native coding agent
  status ID                       Read task and VM status
  activate ID                     Resume VM and restore latest Codex auth
  checkpoint ID                   Save auth, pause VM, release profile lease
  reconcile ID                    Reconcile uncertain VM creation
  recover-prepare ID              Verify a partially prepared checkout
  recover-turn ID --conversation ID

Configuration: --config YAML, then IB_TICKET_* environment, then command flags.
Task IDs work without an issue tracker. --ticket remains an alias for create --task.
No token values belong in YAML, environment overrides or task prompts.`)
}

func expandHome(path string) string {
	if path == "~" {
		h, e := os.UserHomeDir()
		if e == nil {
			return h
		}
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		h, e := os.UserHomeDir()
		if e == nil {
			return filepath.Join(h, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
func reorder(args []string) ([]string, error) {
	flags, pos := []string{}, []string{}
	for n := 0; n < len(args); n++ {
		s := args[n]
		if strings.HasPrefix(s, "-") {
			flags = append(flags, s)
			if !strings.Contains(s, "=") && s != "--help" && s != "-h" {
				n++
				if n >= len(args) {
					return nil, errors.New("flag requires a value")
				}
				flags = append(flags, args[n])
			}
		} else {
			pos = append(pos, s)
		}
	}
	return append(flags, pos...), nil
}
func execute(args []string) (any, error) {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		return map[string]string{"version": Version}, nil
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		help()
		return nil, nil
	}
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".local", "state", "instabox-ticket-runner")
	configPath := os.Getenv("IB_TICKET_CONFIG")
	if v := os.Getenv("IB_TICKET_STATE"); v != "" {
		root = v
	}
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		name, val, inline := strings.Cut(args[0], "=")
		if name != "--state" && name != "--config" {
			return nil, errors.New("global options must be --config or --state")
		}
		args = args[1:]
		if !inline {
			if len(args) == 0 {
				return nil, errors.New("missing global option value")
			}
			val = args[0]
			args = args[1:]
		}
		if name == "--state" {
			root = val
		} else {
			configPath = val
		}
	}
	if len(args) == 0 {
		return nil, errors.New("missing command")
	}
	action := args[0]
	c, e := loadConfig(expandHome(configPath), os.LookupEnv)
	if e != nil {
		return nil, e
	}
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	user := f.String("user", c.User, "")
	org := f.String("org", c.Org, "")
	provider := f.String("provider", c.Provider, "")
	repo := f.String("repo", c.Repo, "")
	base := f.String("base", c.Base, "")
	snapshot := f.String("snapshot", c.Snapshot, "")
	cpu := f.Int("vcpus", c.VCPUs, "")
	memory := f.Int("mem-mib", c.Memory, "")
	ticketFlag := f.String("ticket", "", "")
	taskFlag := f.String("task", "", "")
	prompt := f.String("prompt-file", "", "")
	cwd := f.String("cwd", "/data/home", "")
	model := f.String("model", "", "")
	conv := f.String("conversation", "", "")
	backend := f.String("credential-backend", c.CredentialBackend, "")
	keyFile := f.String("credential-key-file", c.CredentialKeyFile, "")
	reordered, e := reorder(args[1:])
	if e != nil {
		return nil, e
	}
	if e = f.Parse(reordered); e != nil {
		if e == flag.ErrHelp {
			help()
			return nil, nil
		}
		return nil, errors.New("invalid command arguments")
	}
	c.User = *user
	c.Org = *org
	c.Provider = *provider
	c.Repo = *repo
	c.Base = *base
	c.Snapshot = *snapshot
	c.VCPUs = *cpu
	c.Memory = *memory
	c.CredentialBackend = *backend
	c.CredentialKeyFile = *keyFile
	if e = c.Validate(); e != nil {
		return nil, e
	}
	if action == "create" && *taskFlag != "" {
		if *ticketFlag != "" && *ticketFlag != *taskFlag {
			return nil, errors.New("--task and legacy --ticket must identify the same task")
		}
		*ticketFlag = *taskFlag
	}
	if action == "init-key" {
		if len(f.Args()) != 0 || c.CredentialBackend != "encrypted-file" {
			return nil, errors.New("init-key requires encrypted-file configuration and no positional arguments")
		}
		if e := initCredentialKey(expandHome(c.CredentialKeyFile)); e != nil {
			return nil, e
		}
		return map[string]string{"status": "key-created"}, nil
	}
	valid := map[string]bool{"create": true, "bootstrap": true, "status": true, "prepare": true, "run": true, "activate": true, "checkpoint": true, "reconcile": true, "recover-prepare": true, "recover-turn": true}
	if !valid[action] {
		return nil, errors.New("unknown command")
	}
	ticket := ""
	if action != "create" && action != "bootstrap" {
		ticket, e = requireArg(f.Args())
		if e != nil {
			return nil, e
		}
	} else if len(f.Args()) > 0 {
		return nil, errors.New("unexpected positional arguments")
	}
	s, e := openState(expandHome(root))
	if e != nil {
		return nil, e
	}
	defer s.Close()
	var secrets Secrets = &OSSecrets{service: "instabox-ticket/" + s.Namespace}
	if c.CredentialBackend == "encrypted-file" {
		secrets = &EncryptedSecrets{KeyFile: expandHome(c.CredentialKeyFile), Dir: filepath.Join(s.Root, "credentials"), Namespace: s.Namespace}
	}
	r := Runner{State: s, Client: Instabox{}, Secrets: secrets}
	switch action {
	case "create":
		return r.Create(*ticketFlag, c, false)
	case "bootstrap":
		return r.Bootstrap(c)
	case "status":
		return r.Status(ticket)
	case "activate":
		return r.Activate(ticket)
	case "checkpoint":
		return r.Checkpoint(ticket)
	case "reconcile":
		return r.Reconcile(ticket)
	case "prepare":
		return r.Prepare(ticket, c.Repo, c.Base)
	case "recover-prepare":
		return r.RecoverPrepare(ticket)
	case "recover-turn":
		return r.RecoverTurn(ticket, *conv)
	case "run":
		if *prompt == "" {
			return nil, errors.New("provide a private task-text file using --prompt-file")
		}
		raw, e := os.ReadFile(expandHome(*prompt))
		if e != nil {
			return nil, errors.New("cannot read task-text file")
		}
		return r.Run(ticket, string(raw), *cwd, *model)
	}
	return nil, errors.New("unknown command")
}
func main() {
	v, e := execute(os.Args[1:])
	if e != nil {
		raw, _ := json.Marshal(map[string]string{"error": e.Error()})
		fmt.Fprintln(os.Stderr, string(raw))
		os.Exit(1)
	}
	if v != nil {
		raw, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			fmt.Fprintln(os.Stderr, `{"error":"cannot encode result"}`)
			os.Exit(1)
		}
		fmt.Println(string(raw))
	}
}
