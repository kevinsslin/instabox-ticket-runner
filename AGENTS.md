# Contributor instructions

Keep the CLI independent of issue trackers and bot vendors. Task IDs are opaque;
company values and account configuration belong in private YAML, never examples.
Preserve existing state schemas and command aliases when changing public names.

Preserve credential versions, leases and uncertain-operation recovery. Never log
credentials, bypass a lock or delete ambiguous work. Changes to credential storage
need failure-path tests that verify the guest copy and lease survive errors.

Run `go test ./...` and `go vet ./...` for Go changes. Build and check the skill
wrapper after CLI changes. The duration example is deliberately incomplete and
has its own module. Use local link checks for documentation-only changes.

README owns quickstart, current limitations and bot onboarding; docs/operations.md
owns configuration and recovery; SECURITY.md owns the threat model. Keep audits,
release checklists and implementation diaries in issues or release notes. Release
only after real phone-to-bot task, checkpoint, revision and PR proof; distinguish
that evidence from unit/CI checks. Do not publish private config, state or binaries.
