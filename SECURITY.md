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

Report vulnerabilities privately to the repository owner, using GitHub private
vulnerability reporting when available. Do not post credentials or private state
in public issues. No guaranteed response time is offered.
