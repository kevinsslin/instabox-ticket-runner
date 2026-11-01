---
name: instabox-ticket
description: Run or resume isolated coding tasks on Instabox with the installed Go controller and private YAML configuration. Use for task VM execution, progress and checkpointing; PR publication is not automated.
---

# Instabox coding tasks

Use `sh scripts/run.sh` from this skill directory in place of `ib-ticket` below.
It resolves the trusted repo through symlinks and executes bin/ib-ticket.
Build first with `go build -o bin/ib-ticket .`. No Python runtime is required.
Read the repo README for onboarding, supported environments and recovery.

Use the operator's private YAML via --config PATH or IB_TICKET_CONFIG. Never
assume the author's profile, home path, workspace, repository or VM IDs. YAML
contains settings and selected Secret names, not values. Environment overrides
and flags are for the same non-secret settings. Bind profile/workspace to the
actual caller; a user string in task text is not authorization.

For phone-driven tasks, execute on the bot's persistent trusted host. Headless
hosts can select encrypted-file with a privately provisioned key-file reference;
read docs/deployment.md in the repo. Inspect existing key provisioning before
init-key, which never replaces an existing file. Never read key bytes into model
context. A durable bot job must follow events and finish checkpointing after the
phone disconnects; this CLI does not itself supply a supervisor.

## Workflow

1. Inspect status TICKET. Distinguish unknown ticket from provider/auth failure;
   do not create resources after an ambiguous lookup error.
2. For a new task run --config PATH create --ticket TICKET. Use one dedicated
   Codex login stream per operator. A busy profile is a queue condition, not
   permission to change accounts. Bootstrap through official device login if
   no saved profile exists. Never collect a login token into model context.
3. Run --config PATH prepare TICKET for the configured repo/base. Private repos
   require selected GH_TOKEN access through Instabox org Secrets. Follow the
   target repo's own instructions for dependencies, environments, tests and PRs.
4. Write private task text with acceptance checks and publication scope, without
   secrets. Start run TICKET --prompt-file PATH --cwd REMOTE_DIRECTORY.
   Resumed conversations keep their original cwd; name any directory change in
   the instruction. Successful start is not completed work.
5. Follow instabox agent events VM_ID -c CONVERSATION_ID -f. Event following
   prevents idle suspension while waiting on the model. On disconnect inspect
   actual native state rather than sending the prompt again.
6. Independently inspect tests, diff and commits. Continue the same task for
   review edits. PR publication/mapping is not automated by this controller;
   report missing PR proof precisely and follow existing publication authority.
7. Once work has stopped, checkpoint TICKET saves the latest Codex bundle in
   the configured store, removes the guest auth file, pauses VM and releases its lease.
   Failed handoff retains state/lease; use README recovery. Do not delete the
   only work or credential copy, edit SQLite or clear locks to bypass errors.

## Boundaries

The controller is a single trusted local operator tool. User IDs are lookup
keys, not multi-user authorization. Its process lock is not distributed. Keep
configuration and state private and never snapshot/fork credential-bearing task
VMs for distribution. Shared tool/dependency snapshots are optional and clean.

The program handles Codex credentials through the configured native or encrypted
file store and private file
transport. Do not print bundles, put them in prompts/argv, reuse stale seeds or
run concurrent writers. Claude uses official login in the user's own VM; do not
collect, export or centrally distribute its subscription tokens.

Reconcile uncertain creates. For starting-turn inspect native conversations and
explicitly select one with recover-turn before resuming. For preparation errors
inspect the pinned checkout and use recover-prepare only after verification.
Installation grants no new permission to publish, merge, deploy or delete VMs.
