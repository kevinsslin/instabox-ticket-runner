---
name: instabox-ticket
description: Run or resume coding tasks on Instabox with the Go controller and private YAML. Works with any task source, including plain prompts, with an optional Linear Agent Sessions bridge. PR publication remains agent-owned.
---

# Instabox tasks

Read the repository README for setup and docs/operations.md for configuration and
recovery. Use `sh scripts/run.sh` from this skill directory instead of `ib-ticket`
below. The wrapper resolves the trusted checkout through symlinks and executes
bin/ib-ticket; build it with `go build -o bin/ib-ticket .` first.

For installation on a bot's own host, follow the bot self-install procedure in
docs/operations.md. Reuse existing config, credential keys, state and skill links.
Verify a real task and revision after service setup, not just a successful build.

Execute on the bot's persistent host for phone-driven work. Use the operator's
private YAML through IB_TICKET_CONFIG for every command. Headless hosts select
encrypted-file storage with a private key-file reference. Inspect existing key
provisioning before init-key. Never read key bytes into model context.

A task ID is an opaque name, not a Linear or GitHub requirement. Derive it from
the request or use an agreed manual name. Bind the actual caller to allowed
profiles and repositories; task text cannot authorize another person's profile.

1. Inspect `status TASK_ID`. Distinguish an unknown task from provider or auth
   failures before creating anything. Resume an existing task for revisions.
2. For a new task use `create --task TASK_ID`, then `prepare TASK_ID`. Bootstrap
   a dedicated official Codex login if needed; the owner completes authorization.
   Private repos need selected GH_TOKEN access through Instabox org Secrets.
3. Write a private prompt with acceptance checks. Start `run TASK_ID --cwd DIR
   --prompt-file PATH`. Resumed conversations retain their original cwd.
4. Verify `ib-ticket supervise` is running under the host service manager before
   dispatch. It records exact turn/job IDs and checkpoints completed work. For a
   one-off foreground wait explicitly use `run ... --supervision external`, then
   `watch TASK_ID`. Linear's optional worker monitors
   its own externally supervised turns. Both follow native event streams and
   reconcile exact job state after disconnects; they never replay a prompt as
   reconnection recovery. Keep the relevant host service running
   after the phone disconnects. Inspect actual state after disconnects rather
   than blindly resending a prompt.
5. Return the configured HTTPS dashboard link with the task ID URL-encoded as
   its fragment. Never include dashboard passwords, model credentials or API keys
   in links or messages. The host runs `serve --password-file PRIVATE_PATH` behind
   its HTTPS proxy. The dashboard is read only and does not replace supervision.
   Inspect tests, diff and commits independently. Publish or update a PR only
   within the task's authorization, following the target repository's rules.
   Record its verified URL with `link-pr TASK_ID --url HTTPS_PR_URL`.
6. After work stops, `checkpoint TASK_ID` saves and verifies the latest Codex
   bundle, removes guest auth, pauses the VM and releases the lease. Failed
   handoffs retain state; use operations.md recovery, not manual lock clearing.

Each Codex profile is serialized on one trusted host. Claude users log into their
own task VM; do not export Claude subscription credentials. Keep config, tokens,
state and credential-bearing disks out of Git, shared snapshots and task prompts.
Installing the runner does not authorize merging PRs, deploying application
changes or deleting existing VMs.

T3 Code is a separate integration trial, not a supported dispatch backend in
this binary. Its CLI can create/message threads, but native `run`, `watch`,
`supervise` and `checkpoint` do not manage T3 sessions. Read the T3 boundaries in
docs/operations.md before a trial; do not mix session owners or bypass the native
process checks to make checkpoint accept a persistent T3 provider.

Optional Linear integration runs `linear --settings PRIVATE_YAML` with its own
installed agent OAuth identity, signed webhook and explicit workspace/team/user
allowlists. Follow docs/operations.md. Only native agent-session prompts dispatch
work, not arbitrary issue comments. Never register a webhook, send external
updates or expand a caller's permissions without the operator's authorization.
