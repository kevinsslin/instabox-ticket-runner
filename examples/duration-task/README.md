# Try a coding task

This standalone Go module is intentionally unfinished. It is excluded from root
controller tests and can be used without an issue tracker or a GitHub PR.

Copy it to an unused directory in a task VM, initialize a working branch and commit,
and confirm `go test ./...` fails. Give the coding agent this request:

> Implement Parse in duration.go. Accept a nonnegative ASCII integer followed
> immediately by ms, s or m, stripping surrounding whitespace. Return whole
> milliseconds as int64. Reject invalid text and overflow with an error. Run
> go test ./..., retain existing coverage, add boundary tests and commit on the
> current branch. No network, push or PR is required.

Inspect tests and diff independently, then checkpoint. Resume the same task and
conversation with a request to add lowercase hour support; check the result and
checkpoint again. This exercises coding and review revisions, not PR publication.
