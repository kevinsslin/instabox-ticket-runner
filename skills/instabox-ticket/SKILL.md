---
name: instabox-ticket
description: Run or resume coding tasks on Instabox with the Go controller and private YAML. Works with any task source, including plain prompts; issue-tracker integration and PR publication are not automated.
---

# Instabox tasks

Read the repository README for setup and docs/operations.md for configuration and
recovery. Use `sh scripts/run.sh` from this skill directory instead of `ib-ticket`
below. The wrapper resolves the trusted checkout through symlinks and executes
bin/ib-ticket; build it with `go build -o bin/ib-ticket .` first.

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
4. Use the bot's durable job facility to follow `instabox agent events VM_ID
   -c CONVERSATION_ID -f`, including after the phone disconnects. The controller
   does not supply a supervisor. Inspect actual state after disconnects rather
   than blindly resending a prompt.
5. Inspect tests, diff and commits independently. Publish or update a PR only
   within the task's authorization, following the target repository's rules.
6. After work stops, `checkpoint TASK_ID` saves and verifies the latest Codex
   bundle, removes guest auth, pauses the VM and releases the lease. Failed
   handoffs retain state; use operations.md recovery, not manual lock clearing.

Each Codex profile is serialized on one trusted host. Claude users log into their
own task VM; do not export Claude subscription credentials. Keep config, tokens,
state and credential-bearing disks out of Git, shared snapshots and task prompts.
Installation grants no authority to merge, deploy or delete VMs.
