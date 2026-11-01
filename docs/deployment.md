# Phone-operated bot deployment

The phone is the control surface. The bot receives an authenticated request,
selects the authorized operator's private settings and invokes the controller on
a persistent trusted host. That host owns task metadata, profile leases and
credential-store access. Instabox owns the task VM and coding conversation.
The operator's laptop must be able to stay offline throughout ordinary work.

The current CLI and skill are building blocks, not a deployed bot service. The
desktop adapters do not yet provide the required headless account-bundle backend.
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
desktop prompts. A server backend still needs encrypted, access-controlled,
versioned bundle storage, a noninteractive service identity and verified
write/read-back. Its boot-time unlock or key-management mechanism must be tested
on the selected host. The existing desktop stores cannot simply be relabeled as
that backend. No server implementation or deployment is claimed here.

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
