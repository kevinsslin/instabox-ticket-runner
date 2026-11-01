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

Natural token expiry/refresh, end-to-end Go auth/coding revision proof, private
repo access, real project checks, PR publication, actual bot discovery/invocation,
second-user onboarding, Linux credential-store operation, cancellation, credential
retirement, VM cleanup and cost accounting. Do not present the public repository
as a completed hosted bot service or production-ready automated PR workflow.
