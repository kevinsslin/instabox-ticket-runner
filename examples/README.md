# Reproducible coding trial

The standalone Go module duration-task contains an intentionally unimplemented
function. It is excluded from the root controller's go test ./... traversal.
Push it into an unused task VM directory, initialize a trial branch and commit,
confirm go test ./... fails, then ask the coding agent to follow TASK.md.
Verify tests and diff independently. Checkpoint/pause, request lowercase hour
support in the same directory/conversation, verify tests and checkpoint again.
This proves coding and revisions, not actual GitHub PR publication.
