# Instabox ticket runner

This repository owns a generic Go controller and operator skill. Keep account,
workspace, repository, branch and company-specific values in private YAML or
IB_TICKET_* environment settings. Only generic examples belong in Git.

Preserve task state, credential versions and durable leases. Never log tokens,
bypass locks, clone credential-bearing VM snapshots, or delete ambiguous work.
The OS store adapter must not put credential bytes in subprocess arguments.

Run go test ./... and go vet ./... for code changes. The standalone example
module is deliberately incomplete and excluded from the controller module.
Run the skill wrapper after changes. Keep unit, live VM, coding, PR and
long-term refresh evidence separate. Do not claim full production readiness.

The current controller runs on one trusted macOS/Linux host. It is not a
multi-tenant service and its process lock is not distributed. Publication work
must not include local configuration, databases, credential files or binaries.
