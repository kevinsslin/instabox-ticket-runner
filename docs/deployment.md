# Phone-operated bot deployment

The phone is the control surface. The bot receives an authenticated request,
selects the authorized operator's private settings and invokes the controller on
a persistent trusted host. That host owns task metadata, profile leases and
credential-store access. Instabox owns the task VM and coding conversation.
The operator's laptop must be able to stay offline throughout ordinary work.

The current CLI and skill are building blocks, not a deployed bot service.
An encrypted-file adapter supports headless operation with a privately provisioned
key file. The native Keychain and GNOME adapters remain available for desktops.
Do not configure a server to allow every program to read a desktop keychain or
copy tokens into a bot message to work around this gap.

## Host and dispatch

First identify where the bot actually executes commands. If it already runs on
a persistent trusted host, the CLI can run there under the operator's restricted
service account. A separate host needs an authenticated private execution path.
No new public HTTP endpoint is required merely because the request came from a
phone. This repository currently provides neither a remote dispatch API nor
hosted multi-user authentication.

Persist private YAML and SQLite state outside the checkout. Use one controller
host per state namespace because its process lock is not distributed. Bind the
caller to an allowed profile and repository in trusted bot configuration; task
text cannot choose another person's credentials. Secrets never enter YAML.

## Credentials and continuing work

For dedicated Codex account login, the owner completes the official flow once
and again when reauthorization is required. Routine dispatch must not depend on
desktop prompts. Configure the actual bot host to use encrypted-file storage:

```yaml
credential_backend: encrypted-file
credential_key_file: /absolute/private/service-keys/master.key
```

Replace the example path with a private path owned by the service account,
outside the checkout and encrypted bundle directory. On a new host, initialize
it once with `ib-ticket --config /private/bot.yaml init-key`. This generates
32 binary random bytes without printing them and refuses existing paths. Parents
must be private directories. The command never prints a credential. Alternatively,
provision a 32-byte key using the host's secret manager or service credential mount;
do not run init-key against a managed read-only mount.

Bundles are authenticated encrypted files in the private state directory. Protect
the key separately from their storage and backups. Both key provisioning at boot
and backup/recovery must be verified on the selected host. No automatic key
rotation, remote KMS, or cross-backend login migration is provided.

For a new controller, bootstrap a dedicated official login on that host and have
the owner complete the device flow from their browser. Checkpoint stores the
bundle using the configured backend. Do not copy an actively used laptop cache
or switch an existing profile's backend and assume its versions have migrated.
Configuration and bundle access use the actual trusted bot service identity.
No deployment on a real bot host is claimed by the adapter's local tests.

Retain serialized profile leases through execution and checkpointing. The host
must supervise events, detect completion, inspect results and checkpoint even
after the phone disconnects. The CLI starts background turns, but it does not
currently provide this supervisor. A host restart must reconcile actual VM and
agent state before resuming or handing off credentials.

Claude subscriptions continue to use the original CLI's owner login inside each
task VM. Do not export them into the central Codex bundle store.

## Acceptance

From a phone, dispatch a task, disconnect, inspect progress, obtain one PR, then
request a revision on the same VM/branch/conversation/PR. Keep the laptop offline.
Verify background monitoring and checkpointing, host restart recovery and official
reauthorization behavior. Record the provider and credential backend actually
tested. Desktop CI success is separate evidence. See release.md for publication
gates and VALIDATION.md in the repository root for outstanding limitations.
