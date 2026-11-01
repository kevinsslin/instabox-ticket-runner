package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	User              string   `yaml:"user"`
	Org               string   `yaml:"org"`
	Provider          string   `yaml:"provider"`
	Repo              string   `yaml:"repo"`
	Base              string   `yaml:"base"`
	Snapshot          string   `yaml:"snapshot"`
	VCPUs             int      `yaml:"vcpus"`
	Memory            int      `yaml:"mem_mib"`
	Secrets           []string `yaml:"secrets"`
	CredentialBackend string   `yaml:"credential_backend"`
	CredentialKeyFile string   `yaml:"credential_key_file"`
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}$`)
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var secretPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func validID(s string) error {
	if !idPattern.MatchString(s) {
		return errors.New("invalid identifier")
	}
	return nil
}

func decodeConfig(r io.Reader) (Config, error) {
	c := Config{Provider: "codex", Base: "main", VCPUs: 2, Memory: 4096}
	d := yaml.NewDecoder(r)
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil && err != io.EOF {
		return c, errors.New("invalid YAML configuration; use known non-secret fields only")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, errors.New("use a single YAML document")
	}
	return c, nil
}

func loadConfig(path string, env func(string) (string, bool)) (Config, error) {
	c := Config{Provider: "codex", Base: "main", VCPUs: 2, Memory: 4096}
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return c, errors.New("cannot open configuration file")
		}
		defer f.Close()
		v, err := decodeConfig(f)
		if err != nil {
			return c, err
		}
		c = v
	}
	for k, p := range map[string]*string{"USER": &c.User, "ORG": &c.Org, "PROVIDER": &c.Provider, "REPO": &c.Repo, "BASE": &c.Base, "SNAPSHOT": &c.Snapshot, "CREDENTIAL_BACKEND": &c.CredentialBackend, "CREDENTIAL_KEY_FILE": &c.CredentialKeyFile} {
		if v, ok := env("IB_TICKET_" + k); ok {
			*p = v
		}
	}
	for k, p := range map[string]*int{"VCPUS": &c.VCPUs, "MEM_MIB": &c.Memory} {
		if v, ok := env("IB_TICKET_" + k); ok {
			n, e := strconv.Atoi(v)
			if e != nil {
				return c, fmt.Errorf("IB_TICKET_%s must be an integer", k)
			}
			*p = n
		}
	}
	if v, ok := env("IB_TICKET_SECRETS"); ok {
		c.Secrets = nil
		for _, s := range strings.Split(v, ",") {
			if strings.TrimSpace(s) != "" {
				c.Secrets = append(c.Secrets, strings.TrimSpace(s))
			}
		}
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	switch c.CredentialBackend {
	case "", "native":
		if c.CredentialKeyFile != "" {
			return errors.New("credential_key_file requires encrypted-file backend")
		}
	case "encrypted-file":
		if !filepath.IsAbs(expandHome(c.CredentialKeyFile)) {
			return errors.New("encrypted-file backend requires absolute credential_key_file path")
		}
	default:
		return errors.New("credential_backend must be native or encrypted-file")
	}
	for _, s := range []string{c.User, c.Org} {
		if s != "" {
			if e := validID(s); e != nil {
				return e
			}
		}
	}
	if c.Provider != "codex" && c.Provider != "claude" {
		return errors.New("provider must be codex or claude")
	}
	if c.Repo != "" && !repoPattern.MatchString(c.Repo) {
		return errors.New("repo must be OWNER/REPO")
	}
	if c.Base == "" || strings.HasPrefix(c.Base, "-") || strings.ContainsAny(c.Base, "\r\n\x00") {
		return errors.New("invalid base branch")
	}
	if c.VCPUs <= 0 || c.Memory <= 0 {
		return errors.New("VM size must contain positive integers")
	}
	for _, s := range c.Secrets {
		if !secretPattern.MatchString(s) {
			return errors.New("secrets must contain environment names, never values")
		}
	}
	return nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
