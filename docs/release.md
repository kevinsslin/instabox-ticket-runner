# Release v0.1.0

Status: candidate, not published. A version command or passing CI is not a release.
The initial distribution is source only. No credential-bearing binary bundle,
configuration, database, VM disk or snapshot belongs in release assets.

## Required before publishing

- Review lifecycle transitions, lease retention, uncertain-operation recovery,
  credential transport and OS-store write/read-back behavior.
- Pass tests with race detection, vet, build, wrapper and dependency vulnerability
  checks for the exact commit on macOS and Ubuntu. Test native stores with only
  synthetic credentials in an isolated session; no real credentials in CI.
- Inspect the Git tree and archive for private configuration, credential values,
  company-specific content and accidental local artifacts.
- Build from a fresh clone at the exact candidate commit.
- Using a dedicated official Codex login, run the Go controller through restore,
  real coding, independent test inspection, checkpoint and same-task revision.
  Record actual results without private task IDs or auth data.
- Verify actual bot invocation and one real ticket/branch/PR flow. Retain the VM
  through review and verify the update reaches the same PR.
- Perform dispatch, progress lookup and review revision from a phone, with the
  operator's laptop offline. A persistent host must monitor and checkpoint work.
- Verify the controller's headless credential backend across a host restart,
  without a desktop unlock prompt or manual token transfer through chat. Native
  Keychain/GNOME synthetic tests alone do not satisfy this deployment gate.
- Resolve any credential-access blocker through native user authorization. Never
  bypass OS access controls or replace the user's login to obtain a green check.

Natural token refresh, second-user onboarding and Claude support remain separate
claims. They must either have evidence or be explicitly listed as unverified in
the release notes. A release cannot imply a completed multi-user hosted service.

## Publication

Prepare a draft GitHub release against the reviewed commit, including capabilities,
requirements, evidence and known limitations. Keep it draft while a required gate
is pending. After gates pass, update VALIDATION.md, rerun checks for the resulting
commit, create v0.1.0 at that exact commit and publish its source release. Verify
the public tag, release page and source archive match. Subsequent changes need
a new version; never silently move a published tag.
