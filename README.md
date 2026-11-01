# Instabox ticket runner

A Go CLI and portable agent skill for isolated coding tasks on Instabox.
One task owns one VM, working branch and conversation. Pause between revisions
and continue in the same environment. Each operator uses their own accounts.

**Experimental.** See [validation](VALIDATION.md) for the precise proof and limits.
No account-specific configuration, credentials or VM snapshots are distributed.

The intended bot workflow starts from a phone. A persistent controller host must
handle dispatch, monitoring and checkpointing without the operator's laptop.
Headless hosts can select encrypted-file bundle storage with a separately
provisioned private key file. Background supervision and the complete mobile
bot workflow are not yet verified on a real bot host.
See [deployment boundaries](docs/deployment.md) before setting up a bot host.

## Install

Requires an authenticated Instabox CLI and macOS or Linux. Choose a native desktop
credential store or the headless encrypted-file backend. Build with Go 1.26.10+;
macOS builds need CGO and Xcode Command Line Tools for native Keychain access.
No Python, uv or Node runtime is required by the controller or skill.

```sh
git clone https://github.com/kevinsslin/instabox-ticket-runner.git
cd instabox-ticket-runner
go build -o bin/ib-ticket .
go test ./...
./bin/ib-ticket --help
./bin/ib-ticket --version
```

The binary still delegates VM operations to the installed Instabox CLI.
No prebuilt release or Windows support is claimed. Linux native storage needs a reachable
Secret Service with an existing unlocked GNOME login collection. Item names
include the controller namespace. Do not fall back to plaintext on a headless host.
Keep the checkout at a stable trusted path. Rebuilding or moving a macOS binary
can require fresh native Keychain approval. Never change an item's access rules
to allow all programs. See [release gates](docs/release.md) and
[credential boundaries](SECURITY.md).

## Private configuration

Copy config.example.yaml outside the repo, for example to
~/.config/instabox-ticket-runner/config.yaml, and fill in your settings.
Token values do not belong in YAML or IB_TICKET_* variables. The secrets list
contains only names of existing Instabox org Secrets.

For a bot on its own host, set credential_backend to encrypted-file and
credential_key_file to an absolute private file path. Initialize a new key with
init-key on that host, or provision it through the host's secret manager. No key
bytes are printed. Do not replace a key used by existing bundles. The default
native backend retains the desktop behavior. See docs/deployment.md.

```yaml
user: your-profile
org: your-workspace-id
provider: codex
repo: owner/private-repo
base: main
vcpus: 2
mem_mib: 4096
secrets: [GH_TOKEN]
```

Precedence: built-in defaults, YAML, IB_TICKET_* environment, then command flags.
Use --config PATH or IB_TICKET_CONFIG to select YAML. Supported settings map to
IB_TICKET_USER, ORG, PROVIDER, REPO, BASE, SNAPSHOT, VCPUS, MEM_MIB and SECRETS
under the same prefix. IB_TICKET_SECRETS is a comma-separated list of names.
IB_TICKET_STATE selects the state directory. There is no automatic .env loader.
IB_TICKET_CREDENTIAL_BACKEND and IB_TICKET_CREDENTIAL_KEY_FILE select the store
and key reference; --credential-backend and --credential-key-file override them.
Unknown YAML fields and multiple documents are rejected, without echoing values.
Use a permitted VM size. A clean dependency snapshot is optional.

## Official Codex login

```sh
./bin/ib-ticket --config /private/config.yaml bootstrap
instabox ssh VM_ID -- 'codex login --device-auth'
```

Bootstrap creates a dedicated clean login VM and returns its ID. Complete the
official authorization yourself. After login exits, checkpoint the returned
AUTH task with ./bin/ib-ticket checkpoint AUTH-YOUR_PROFILE_NAME.
Bootstrap does not select repository Secrets or use a configured snapshot.
Do not copy an actively used local Codex cache into concurrent runners.

## Dispatch and resume

```sh
./bin/ib-ticket --config /private/config.yaml create --ticket TASK-123
./bin/ib-ticket --config /private/config.yaml prepare TASK-123
./bin/ib-ticket run TASK-123 --cwd /data/home/workspace --prompt-file /private/task.txt
./bin/ib-ticket status TASK-123
instabox agent events VM_ID -c CONVERSATION_ID -f
./bin/ib-ticket checkpoint TASK-123
```

Prepare clones the configured repo/base and creates a task branch. Private repos
need a selected org Secret named GH_TOKEN; provision it using Instabox's hidden
input and allow github.com and api.github.com. Never copy a broad controller
GitHub login into a VM. Follow the target repo's own setup, test and PR rules.
No production configuration is automatically loaded.

Run starts a background turn, not a completion claim. Follow events while it
runs because model waiting can count as idle. Inspect tests and diffs yourself.
Later runs retain the same conversation and its original cwd; include any
intended directory change in the prompt. PR publishing/mapping is not automated.

Checkpoint refuses running or unknown jobs/agents, saves the latest Codex bundle
and verifies identical read-back from the configured store before removing guest auth,
pausing the VM and releasing its profile. It resumes an idle-paused VM first
because process listings while paused can be cached.
Next run restores the latest bundle. File deletion is not secure disk erasure;
do not share credential-bearing task disks or snapshots as clean images.

## Use with your bot

The portable skill is skills/instabox-ticket/SKILL.md. Link its directory into
your agent's supported discovery path. The tested local Grok path is
~/.grok/skills/; preserve an existing installation. The shell wrapper resolves
this checkout through symlinks and runs bin/ib-ticket. Build the binary first.
See [the onboarding prompt](docs/grok-onboarding.md). Verify an actual bot status
call before assigning a real ticket; skill installation is not integration proof.

## Credential and execution boundaries

SQLite stores metadata/version references/leases under
~/.local/state/instabox-ticket-runner (0700). Token bytes live in the selected
native store or authenticated encrypted files in that directory's credentials subdirectory;
private temporary files and Instabox's file transport move bundles. They do not
enter command arguments, task prompts or logs. Task text reaches the Instabox CLI
through stdin; native conversation/event files still contain it inside the VM.
Plaintext fallback is disabled.
Preserve the DB and namespace; do not manually clear a failed lease.

Each Codex profile runs serially. Use one trusted controller host: --user is a
lookup key, not authentication, and the process lock is not distributed. The
controller must be online for dispatch/checkpoint. Scheduling, cancellation,
credential retirement, automatic VM cleanup and hosted multi-user authorization
are pending. More VMs do not create more model account quota.

Claude subscription login stays inside each user's task VM. This tool does not
collect, export or distribute Claude subscription tokens. Use native official
login rather than a central Claude subscription-token store.

## Recovery

- Creating/created: inspect provider state, then reconcile the exact task.
- Preparing: inspect the checkout; recover-prepare verifies pinned origin/branch.
- Restoring: retry activate under the retained lease.
- Starting-turn: inspect native events; recover-turn selects a verified existing
  conversation. Never blindly resend a lost start request.
- Active: activate an idle-paused VM, follow work, checkpoint once stopped.
- Auth-saved/credentials-removed: retry checkpoint to finish the handoff.

## Provider guidance

[Codex private automation auth](https://learn.chatgpt.com/docs/auth/ci-cd-auth)
describes this advanced serialized account flow; API keys remain the general
automation recommendation. Claude has different [authentication](https://code.claude.com/docs/en/authentication)
and [credential-use rules](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use).

MIT licensed. Community workflow; not an official provider product.
