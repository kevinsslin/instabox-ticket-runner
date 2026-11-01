# Verification status

## Current Go implementation

The controller is now Go with private YAML configuration and non-secret
IB_TICKET_* environment overrides. Unit tests cover credential validation,
durable leases, controller locking, live-work handoff refusal, failed OS-store
writes, stale versions, resumable suspension failure, unknown create/start
outcomes, ownership checks, Claude isolation, bootstrap, pinned repo preparation
and configuration rejection/precedence. See go test ./... for current results.

Existing SQLite metadata is compatible. A private local backup was made before
migration and the operator's original OS-store entries were preserved. The new
binary successfully reads existing task metadata and provider VM state. Access
to migrated credentials can require a native Keychain authorization for the new
executable. At migration time native Keychain access was rejected; Go credential
restore, agent continuation and write-back are not yet marked verified.

## Release audit on 2026-11-01

The v0.1.0 candidate adds credential read-back verification before guest deletion,
rejection of incomplete/unknown work-state responses, authoritative activity
checks after waking an idle-paused VM, recovery after create auto-suspension,
Node-based Claude process detection and task text over stdin. Auth bundles with
an API-key override are rejected. Native auth paths are checked before a turn.

Fault-injection and CLI transport tests cover these changes. Local race tests,
vet, macOS build and a synthetic native Keychain write/read/delete passed.
The synthetic test reads no real login and does not establish model execution.
Known jose2go issues were fixed by upgrading to 1.7.0; x/sys was also upgraded.
The candidate requires Go 1.26.10+ so scans and builds use a patched toolchain.
CI additionally exercises an isolated Linux Secret Service with synthetic data.
Consult the exact candidate's CI run for its current result.

The first Linux integration run exposed a graphical collection-creation prompt.
The adapter now uses the existing unlocked GNOME login collection and prefixes
item names with the controller namespace. The native test also checks that a
different namespace cannot read its synthetic item.

Publication remains gated by docs/release.md. A draft release is not a public
version, and the outstanding real-login/bot/PR proof below remains outstanding.

## Earlier prototype observations

The earlier controller performed official remote Codex device login, a model
smoke request, versioned auth save/pause/restore, continuation on the same VM,
and restoration into a second clean VM. A small Python coding fixture was
implemented and revised after pause on the same branch/conversation. Independent
checks passed five test methods after implementation and seven after revision.

Those observations are historical prototype evidence and do not establish Go
runtime behavior. The current public example is a standalone deliberately
unfinished Go module. Its local unit tests are an input to a future remote coding
trial, not part of the controller test suite.

## Still pending

The primary deployment target is a phone-operated bot with a persistent controller
host. It must continue while the operator's laptop is offline. Current macOS
Keychain and unlocked GNOME collection checks validate desktop adapters, not a
headless production deployment. Selection and implementation of the server
credential backend, actual bot dispatch, monitoring and automatic checkpointing
remain required before the mobile workflow can be claimed to work.

Natural token expiry/refresh, end-to-end Go auth/coding revision proof, private
repo access, real project checks, PR publication, actual bot discovery/invocation,
second-user onboarding, Linux credential-store operation, cancellation, credential
retirement, VM cleanup and cost accounting. Do not present the public repository
as a completed hosted bot service or production-ready automated PR workflow.
