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

`run` starts a background turn and records its conversation/job ID before returning.
Run `ib-ticket supervise` as a persistent service on the controller host to
checkpoint completed host-managed turns automatically. `watch TASK_ID` waits for
one task in the foreground instead, after `run ... --supervision external`.
Default `run` refuses to start without a live supervisor process lock, so missing
service setup does not silently leave an unmanaged VM running. The supervisor
attaches a native `agent events -f` follower to each recorded job (up to four
concurrent followers). It reads small conversation metadata at startup and when
a follower exits, then rechecks all conversations, jobs and processes through
checkpoint before stopping. EOF or a non-zero follow exit alone never proves
completion: a failed agent turn can also produce a non-zero follow exit.
Completed failed/interrupted turns can be checkpointed safely after live checks.
The native transcript is drained without accumulating it in the follow process;
final bounded observations remain a separate capture operation.
It never assumes successful agent startup means completed work. Keep the service
running independently of your phone or bot chat connection. There is no scheduler
or automatic PR publisher. The optional Linear worker supervises its own turns
(`--supervision external`) so the generic supervisor will not race its reporting.

New VMs use `auto_suspend: 5m` (paid plans), configurable through YAML or
IB_TICKET_AUTO_SUSPEND. Free plans require `30m` plus an allowed VM size. The
60-minute runtime cap still applies even during active work. Existing VMs retain
their policy; after verifying ownership, an operator can change it with
`instabox auto-suspend VM_ID 5m` (or `30m` for free). Idle pause is a fallback,
not checkpoint: guest credentials and profile leases remain until saved safely.
If a VM pauses before completion, inspect and activate the same task, then run
`watch TASK_ID` to explicitly retry monitoring. Uncertain starts require
`recover-turn` first, which records the verified native job without replaying a prompt.

`supervise --interval 5s` controls only local SQLite task discovery (default 5s,
accepted 5s to 1m), not repeated native status/event requests. The Linear bridge
also checks its local inbox for new/retry work, while its running turns use the
same event-following and reconciliation logic. The browser still polls its
read-only dashboard while visible; this does not drive checkpoint completion.

On a connection failure, reconcile native state and reconnect with exponential
backoff plus jitter (about 1, 2, 4, 8 and 16 seconds, six consecutive attempts).
A connection lasting 30 seconds resets the consecutive-failure budget. There is
no short request timeout on the long-lived follow process. A five-minute safety
reconciliation reads small native metadata while keeping a healthy follower
attached, covering half-open or stalled streams without repeatedly replaying logs.
Disconnects never start a new agent turn or force-resume a paused VM. Controller shutdown cancels
local followers and leaves native running turns intact. Durable SQLite records
allow reattachment after service restart; an already completed job can be
checkpointed without following or replaying it. Do not rely on an in-memory
stream notification as the source of truth.

`agent events -f` is the CLI's documented follow interface; the controller does
not claim that the CLI's internal transport is native server push. Its upstream
implementation may poll. This change removes the controller's repeated full-log
completion polling and uses one follow process per attached turn.

The supervisor marks failed checkpoints as attention and preserves work/leases.
It does not retry these forever or force-pause a possibly active agent. Fix the
underlying issue and explicitly `watch TASK_ID` again. Logs contain task IDs and
generic state messages, not provider error dumps or credential bytes.

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

## Dashboard

Run `ib-ticket serve --password-file /PRIVATE/dashboard-password` on the same
trusted host and state directory as dispatch. The state must already exist.
The password file must be a private regular file, at least 24 random characters,
maximum 4096 bytes. Restart the server after rotating it. Username is `operator`.
This is a single-operator view of all tasks, not per-user authorization.

The default listener is `127.0.0.1:8787`; `--listen` permits only loopback IPs.
For phone access, route a dedicated HTTPS hostname through your existing reverse
proxy to that listener. Preserve Authorization and disable access/header/body
logging of sensitive data. Do not expose plain HTTP remotely. For example, with
Caddy on the same host (replace the hostname and configure DNS first):

```caddyfile
tasks.example.com {
    reverse_proxy 127.0.0.1:8787
}
```

The proxy is deployment configuration, not installed by this repository. Keep the
controller, Instabox CLI session and encrypted-store key on the bot host. The web
server does not open the credential store or hold the controller process lock.
Run it under your existing service manager for persistence. Closing the browser
never interrupts or checkpoints a task. Keep the host supervisor or Linear worker running as a durable service.

Share `https://YOUR_HOST/#TASK_ID`, with the task ID URL-encoded. The browser polls
while visible every 15 seconds. Collection is serialized with a 15-second cache;
provider subprocesses have 20-second timeouts and an 8 MiB response limit. Each
view shows the last 150 events, omits individual events over 32 KiB, and caps the
diff display at 180 KB. Provider errors leave an explicit unavailable notice;
failed refreshes mark previous displayed data stale. Agent messages are evidence,
not proof that tests passed. Command results retain their native exit codes.

Only owned running VMs are inspected. A paused, stopped, unknown or foreign VM
is never resumed or read through guest commands. Diff inspection executes fixed
git commands in the prepared checkout and may extend a running VM's activity.
It does not fetch: the local origin/base may be stale. Untracked file contents
are excluded. Checkpoint saves the latest bounded view in the private state
`observations/` directory before removing guest credentials. Paused tasks show
that saved evidence, its capture timestamp and a historical-view notice. Only the
latest observation per task is retained. Older tasks without a saved observation
show metadata only. The snapshot is private plaintext work, not an encrypted
credential bundle; include it in your host storage/access policy.
The browser cannot run shell commands, alter tasks or submit prompts.

Use `link-pr TASK_ID --url https://github.com/OWNER/REPO/pull/NUMBER` to record a
verified result. This only writes local task metadata, with no GitHub mutation.
The URL must match the prepared repo. No PR creation, test certification or
automatic completion is implied.

## Linear agent

The optional bridge uses Linear's native [Agent Sessions](https://linear.app/developers/agent-interaction).
It is one trusted workspace, fixed controller profile/repository, and explicit
human/team allowlists per installation. It uses the same Go binary and local
SQLite, not another orchestration framework. Register your own OAuth application
and install it with Linear's [agent actor authorization](https://linear.app/developers/oauth-actor-authorization).
Provision the installed agent's OAuth access token and webhook signing secret as
separate private files (mode 0600), never YAML values or chat messages. Refresh
expired access tokens with your existing OAuth provisioning system and replace
the token file atomically. The bridge rereads it for each request. There is no
browser OAuth callback, token refresh flow or public multi-tenant installer in
this version.

Copy `linear.example.yaml` outside the checkout and supply exact Linear UUIDs.
Configure an HTTPS endpoint in your OAuth app for `AgentSessionEvent` webhooks.
Route only `/webhooks/linear` to `127.0.0.1:8788` through your HTTPS proxy:

```caddyfile
agent.example.com {
    handle /webhooks/linear {
        reverse_proxy 127.0.0.1:8788
    }
    respond 404
}
```

Start the bridge on the same controller host/config/state as the dashboard:

```sh
ib-ticket --config /PRIVATE/config.yaml linear --settings /PRIVATE/linear.yaml
```

Use your service manager with restart-on-failure and private umask. A separate
`linear.lock` permits one bridge while dispatch/dashboard remain independent.
Do not run multiple hosts against copied state. Each callback verifies raw-body
HMAC-SHA256, a 60-second timestamp window, workspace, app-user identity, team and
human allowlists. Unsupported event categories and the agent's own output never
dispatch work. Requests are bounded to 512 KiB; prompts to 256 KiB. A stable
session/activity key deduplicates retries; accepted prompts persist in private
`linear.db` before HTTP 200. A session cannot be rebound to another issue.

Delegate or mention the agent, then reply inside its agent conversation. Ordinary
comments elsewhere on the issue are not instructions. Receipt and running status
are agent activities; final visible agent text is a response activity. Monitor
and recorded PR links use session `addedExternalUrls`. Completion checks the exact
job ID, saves the result before checkpoint and confirms checkpoint before claiming
the VM is paused. Large final text is capped at 12 KB with a notice. Detailed
native tools/commands remain on the dashboard. No hidden reasoning is published.
The bridge does not independently discover/open a PR; use `link-pr` after verifying
one. Task IDs are a stable opaque hash of workspace/issue, so revisions reuse the
same VM, branch and conversation across sessions. Prompts are removed from completed
inbox rows; final replies/receipt metadata remain private local history.

Follow-ups wait for earlier turns and checkpoint. Delivery order is durable receipt
order; webhook arrival can differ from human send order. If a turn is uncertain or
checkpoint fails, the bridge reports an error, retains the prompt/work/lease and
blocks later prompts for that task. It never retries a potentially committed start.
On restart, an in-flight dispatch becomes blocked. Other running/finishing receipts
resume monitoring/reporting, using stable activity UUIDs to verify uncertain sends.

For recovery, stop the bridge, inspect the controller/native conversation, and
use existing `reconcile`, `recover-prepare`, `recover-turn` or `activate` commands
as appropriate. To reattach monitoring to the recovered active conversation:

```sh
ib-ticket --config /PRIVATE/config.yaml linear --settings /PRIVATE/linear.yaml \
  --resolve 'prompt:LINEAR_ACTIVITY_UUID'
```

Use `created:LINEAR_SESSION_UUID` for an initial receipt. This verifies current VM
ownership and native job, and never resends the stored prompt. Restart the service
afterward. A request that never started needs an explicit operator decision and
manual recovery/start before reattachment. Never edit database rows or clear leases
to bypass uncertain work. Linear stop/control signals are not implemented and do
not interrupt native work; interrupt on the trusted host if required. API failures
leave receipts pending or in attention; a working token and service are needed
for timely acknowledgement/reporting. Real OAuth/webhook/phone acceptance remains
required before publishing the draft release.
