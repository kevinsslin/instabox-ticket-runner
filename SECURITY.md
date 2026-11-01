# Security

This tool trusts the controller host, its service account, Instabox and the coding
agent inside each task VM. A compromised host or malicious task code can access
credentials available to it. The CLI is for one trusted controller host, not a
multi-tenant service. Its lock is not distributed and profile names are not authentication.

Codex profiles execute serially. Checkpoint reads back saved credentials before
removing guest auth and releasing a lease. Uncertain activity or failed storage
retains the lease and work. Never clear locks to bypass a failed handoff.

The encrypted-file backend uses AES-256-GCM with random nonces and authenticated
namespace/profile/version. It publishes immutable files with sync and private
permissions. The master key is a separate private file. Protect it independently
of bundle backups; encryption does not protect a compromised service account.
There is no automatic key rotation, backend migration or plaintext fallback.
See [operations](docs/operations.md#credential-storage) for provisioning and recovery.

Token bytes and encryption keys must stay out of YAML, prompts, argv, Git and logs.
Claude subscription credentials stay in each task VM. Limit GitHub Secret access,
disable personal Vault injection and use clean snapshots. Deleting guest auth does
not securely erase disk history, snapshots or copies created by task code.
Task prompts and agent events may contain private work; review before sharing.

The optional dashboard uses HTTP Basic authentication with a separate private
password file and binds only to loopback. Remote access requires an HTTPS proxy.
Every route is authenticated; responses are no-store and use a restrictive CSP.
The browser receives task metadata, native events and diffs, never credential-store
bundles. Agent output or repository changes can nevertheless contain secrets;
there is no guaranteed secret redaction. Treat dashboard access as access to all
private work on this controller, and never share the password publicly.
The server has a read-only SQLite connection and no browser mutation endpoints.
It checks VM ownership before collecting guest data and never resumes a VM.
Hostile task code can falsify command output or diffs; inspect PR evidence yourself.

The optional Linear bridge accepts only signed, fresh Agent Session callbacks
for configured workspace/app/team/human IDs. It persists delivery IDs and dispatch
intent to avoid automatic replay after uncertain operations. Its OAuth token and
signing secret stay in private host files. The bridge can execute code with the
configured profile/repository authority; allowlist membership is an authorization
boundary, and issue text cannot choose credentials or another repository. Native
agent output is sent back only to the configured Linear session. Untrusted issue
content can still prompt-inject the coding agent. The native CLI runs with VM
permissions, so scope repository tokens and review changes before merging.
Observation snapshots and Linear inbox/replies contain plaintext private work in
the protected state directory. They are separate from encrypted login bundles.

Report vulnerabilities privately to the repository owner, using GitHub private
vulnerability reporting when available. Do not post credentials or private state
in public issues. No guaranteed response time is offered.
