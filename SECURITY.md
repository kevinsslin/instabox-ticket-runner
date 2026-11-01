# Credential boundaries

This is an experimental tool for one trusted operator on one controller host.
The operator, local OS account, native credential store, Instabox account, guest
VM and coding agent can access sensitive work. Task code runs with the agent's
VM permissions. The controller does not protect credentials from malicious code
inside that VM or a compromised controller host. Do not expose it as a service
for untrusted callers. A profile name is not authentication.

Codex account bundles are versioned in the native OS store. A durable lease
serializes each profile across task VMs on this host. Checkpoint verifies the
new bundle can be read back before deleting guest auth. Unknown activity,
unavailable storage and uncertain remote operations retain the lease and work.
Never clear a lease to work around an error. Native OS approval is required when
the system denies access; there is no plaintext fallback or ACL bypass.

Claude subscriptions use the original CLI's login inside each task VM. This
controller does not collect or export Claude subscription credentials.

YAML and environment overrides hold settings or selected Secret names, never
credential values. The optional GitHub Secret grants repository access separately
from model login. Limit its repositories and allowed HTTPS hosts. Disable Vault
injection and use only clean snapshots. Deleting auth.json does not securely erase
its disk history, copies made by task code or snapshots. Do not distribute task
disks as clean templates. Native prompts, conversation events and job artifacts
can contain private task content; do not publish them indiscriminately.

Use a patched Go toolchain, review dependencies and keep Instabox and the official
agent CLIs current. Preserve private state and native-store versions before an
upgrade. CLI and provider behavior can change independently of this repository.
See VALIDATION.md for what has and has not been exercised.

Report security issues privately to the repository owner, using GitHub private
vulnerability reporting when available. Do not include credentials or private
state in public issues. No guaranteed response time is offered.
