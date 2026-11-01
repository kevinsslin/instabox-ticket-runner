# Operations

The controller runs on your bot's persistent host. It keeps task metadata,
credential versions and leases there; Instabox runs the coding agent. Your phone
is the control surface. No additional public API is needed when the bot can
already execute commands on that host.

## Configuration

Select private YAML with `IB_TICKET_CONFIG` or `--config PATH`. Precedence is
built-in defaults → YAML → `IB_TICKET_*` environment → command flags. The CLI
rejects unknown YAML fields and multiple documents. It does not load `.env` files.

| YAML field | Environment variable | Purpose |
| --- | --- | --- |
| `user` | `IB_TICKET_USER` | Your credential profile name |
| `org` | `IB_TICKET_ORG` | Instabox workspace ID |
| `provider` | `IB_TICKET_PROVIDER` | `codex` or `claude` |
| `repo` | `IB_TICKET_REPO` | GitHub `OWNER/REPO` to prepare |
| `base` | `IB_TICKET_BASE` | Base branch, default `main` |
| `vcpus` | `IB_TICKET_VCPUS` | Allowed VM CPU count |
| `mem_mib` | `IB_TICKET_MEM_MIB` | Allowed VM memory in MiB |
| `snapshot` | `IB_TICKET_SNAPSHOT` | Optional clean dependency snapshot |
| `secrets` | `IB_TICKET_SECRETS` | Existing Instabox Secret names; comma-separated in env |
| `credential_backend` | `IB_TICKET_CREDENTIAL_BACKEND` | `encrypted-file` or `native` |
| `credential_key_file` | `IB_TICKET_CREDENTIAL_KEY_FILE` | Private key path, with `~/` expansion |

`IB_TICKET_STATE` or global `--state PATH` selects the state directory, defaulting
to `~/.local/state/instabox-ticket-runner`. It must be private (0700). Keep the
SQLite database and namespace together. Configuration contains settings and
Secret names, never token or encryption-key values.

Task IDs accept ASCII letters, digits, hyphens and underscores, starting with a
letter or digit. VM naming limits a task ID to 54 characters. IDs are opaque:
`fix-login`, `GH-42` and `my_task_1` have the same behavior. Legacy `--ticket`
remains an alias for `create --task`. Existing JSON, database columns and VM
metadata retain the field name `ticket` for compatibility; no tracker is queried.
Issue fetching, issue updates, task-to-PR mapping and notifications belong to the
calling agent or an optional integration.

## Credential storage

### Headless bot host

Use `credential_backend: encrypted-file`. The example YAML places its key in a
private directory separate from task state. `init-key` generates 32 random binary
bytes, never prints them and never overwrites an existing file. Run it only when
provisioning a new key. Alternatively, supply a key through the host's secret
manager or service credential mount; do not initialize over a managed mount.

Authenticated encrypted bundles live under the state directory's `credentials`
subdirectory. Protect the key separately from bundle storage and backups. Key
loss prevents decryption; replacing a live key does not rotate existing bundles.
Key rotation and migration from another backend are manual operations.

Run the bot under a trusted service account with access to its private config,
state and key. Verify key provisioning after a host restart. The backend needs a
filesystem supporting hard links and file/directory sync.

### Desktop host

Set `credential_backend: native` and remove `credential_key_file`. macOS uses
Keychain and needs CGO/Xcode Command Line Tools when building. Rebuilding or moving
a binary may require native access approval. Linux uses Secret Service with an
existing unlocked GNOME login collection and namespaced item keys. Neither adapter
falls back to plaintext. Use the encrypted-file backend for a headless service.

### Official accounts

Bootstrap creates a clean Codex login VM without repository Secrets or a supplied
snapshot. The owner completes the official device flow; checkpoint saves the
complete CLI-managed bundle. Do not import an actively used laptop auth cache.
The CLI refreshes its own credentials; the controller saves their latest version.

Each Codex profile is leased to one task from activation through checkpoint.
Other tasks using that profile must wait. A profile name is a lookup key, not
caller authentication. Bind callers to allowed profiles in trusted bot settings.

Claude users log into the original CLI inside each task VM. Claude subscription
credentials stay there and are not exported to the controller or other VMs.

[Codex account automation](https://learn.chatgpt.com/docs/auth/ci-cd-auth) describes
this advanced serialized flow; API keys remain its general automation recommendation.
Claude has separate [credential-use rules](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use).

## Repository access and task execution

For a private GitHub repo, provision an Instabox org Secret named `GH_TOKEN` using
its hidden input. Restrict repository permissions and allow `github.com` and
`api.github.com`; select its name in YAML. Model login and GitHub authorization
are separate. Do not copy a broad host GitHub login into a task VM.

`prepare` clones the configured repo/base into `/data/home/workspace` and creates
a task branch. Follow that repository's own dependency, test and PR rules. Base
and repository are pinned once prepared. Later `run` calls resume the same
conversation with its original working directory.

`run` starts a background turn. Keep `instabox agent events ... -f` running because
waiting on a model can count as idle. The bot's durable job facility must supervise
completion, inspect results and checkpoint after the phone disconnects. The CLI
has no built-in supervisor, scheduler or automatic PR publisher.

Checkpoint refuses active or unknown work, saves and reads back the bundle, removes
guest Codex auth, suspends the VM and releases the lease. It wakes an idle-paused
VM first to read live process state. Claude login remains in its task VM.

## Recovery

Inspect `status TASK_ID` and native Instabox activity before retrying. A lost start
response may already have launched work. Retain failed leases and the only copy
of work or credentials.

| Task phase | Action |
| --- | --- |
| `creating` / `created` | Inspect provider state, then `reconcile TASK_ID` |
| `preparing` | Check origin and branch; `recover-prepare TASK_ID` verifies the checkout |
| `restoring` | Restore key/store access, then retry `activate TASK_ID` under its lease |
| `starting-turn` | Inspect native conversations, then `recover-turn TASK_ID --conversation ID` |
| `active` | Activate an idle-paused VM, follow work and checkpoint after completion |
| `auth-saved` / `credentials-removed` | Retry `checkpoint TASK_ID` to complete the handoff |

Preserve ambiguous or dirty VMs. Automatic cancellation, credential retirement
and VM cleanup are not implemented. Never share task disks as clean snapshots;
shared base snapshots may contain only tools and public dependencies.
