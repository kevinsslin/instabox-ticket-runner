# Instabox Task Runner

Run coding tasks in isolated Instabox VMs from your bot's own host. Send a task
from your phone, pause the VM between revisions, and continue in the same working
branch and conversation.

```text
Phone → your bot / controller host → Instabox task VM
```

Use any issue tracker or a plain prompt. A task ID is just your chosen identifier,
such as `fix-login` or `GH-42`. No issue-tracker account, API key or integration is
required. PR creation and issue updates are handled by your agent, not this CLI.

**Experimental.** Automated controller, credential-store and build checks pass on
macOS and Linux. The complete phone-to-bot-to-PR workflow, real host recovery and
long-term token refresh still need live verification. v0.1.0 is an unpublished draft.

## Install on your bot's host

Requires Go 1.26.10+ and an installed, authenticated [Instabox CLI](https://instabox.sh).
The controller and skill need no Python runtime.

```sh
git clone https://github.com/kevinsslin/instabox-ticket-runner.git
cd instabox-ticket-runner
go build -o bin/ib-ticket .
export PATH="$PWD/bin:$PATH"
ib-ticket --help
```

## Configure and log in

```sh
mkdir -p "$HOME/.config/instabox-ticket-runner"
chmod 700 "$HOME/.config/instabox-ticket-runner"
export IB_TICKET_CONFIG="$HOME/.config/instabox-ticket-runner/config.yaml"
test -e "$IB_TICKET_CONFIG" || cp config.example.yaml "$IB_TICKET_CONFIG"
chmod 600 "$IB_TICKET_CONFIG"
```

Edit the YAML with your profile, Instabox workspace and repository. The example
uses encrypted storage for a headless host; its key stays outside the checkout.
Keep this configuration in your bot's service environment for every command.

```sh
ib-ticket init-key
ib-ticket bootstrap
instabox ssh VM_ID -- 'codex login --device-auth'
ib-ticket checkpoint AUTH-YOUR_PROFILE
```

Bootstrap returns the login VM ID and auth task ID. Complete the official login
in your browser, then checkpoint that exact task. `init-key` creates a new private
key without printing it and refuses to replace one. Existing installations should
reuse their provisioned key. [Operations](docs/operations.md) covers desktop
stores, private repositories, configuration and recovery.

## Run a task

Write the request and acceptance criteria into a private text file on the host.

```sh
ib-ticket create --task fix-login
ib-ticket prepare fix-login
ib-ticket run fix-login --cwd /data/home/workspace --prompt-file /private/task.txt
ib-ticket status fix-login
instabox agent events VM_ID -c CONVERSATION_ID -f
# With the host supervisor installed, checkpoint runs automatically.
# Inspect one recorded turn (when no supervisor daemon is running):
# Start it with run ... --supervision external, then use:
# ib-ticket watch fix-login
```

Use the VM and conversation IDs returned by the controller. Follow events until
the turn finishes, inspect the diff and tests, then checkpoint. Request revisions
with another `run` for the same task ID. A successful start is not task completion.

Start `ib-ticket supervise` as a persistent host service before dispatching.
It records and watches exact turn/job IDs, then checkpoints automatically after
completion. `run` requires a running supervisor by default and returns immediately; it does
not launch a hidden daemon.
For a foreground alternative, start `run ... --supervision external` followed by
`watch TASK_ID`. Phone disconnection is harmless
when the host service stays running. One Codex profile executes serially.

On Linux with systemd user services:

```sh
mkdir -p "$HOME/.local/bin" "$HOME/.config/systemd/user"
install -m 755 bin/ib-ticket "$HOME/.local/bin/ib-ticket"
cp deploy/ib-ticket-supervise.service "$HOME/.config/systemd/user/"
systemctl --user daemon-reload
systemctl --user enable --now ib-ticket-supervise
systemctl --user status ib-ticket-supervise
```

The template expects the private config at the quickstart path above. Use your
host's existing service manager on other systems. Check service startup before
sending tasks. To survive user logout, the host must support persistent user
services (systemd linger, configured by its administrator).

Completion is checked every 15 seconds, followed by live safety checks, observation
capture and credential storage. There is no fixed stop-time guarantee during
provider/storage failures. New VMs use a 5-minute idle backup on paid plans; free
plans require `auto_suspend: 30m`. The existing 60-minute maximum runtime remains.
Idle/max-runtime pause does not save credentials or release the profile lease.
The host supervisor fails closed and marks attention when completion or storage
is uncertain. Never automatically retry a lost agent start.

## Monitor from your phone

The built-in Go dashboard shows tasks, VM/agent state, native messages and tool
results, command output/exit codes, tracked diffs and a recorded PR link. It is
read only. There is no frontend build or separate database service.

Generate a separate dashboard password on the bot host, outside the checkout:

```sh
umask 077
openssl rand -hex 24 > "$HOME/.config/instabox-ticket-runner/dashboard-password"
ib-ticket serve --password-file "$HOME/.config/instabox-ticket-runner/dashboard-password"
```

Open `http://127.0.0.1:8787` on the host, or put this loopback listener behind your
HTTPS reverse proxy for phone access. Sign in as `operator` with that password.
Keep the password in a password manager; never include it in a task URL or chat.
Give the bot your dashboard's HTTPS base URL and have it return
`https://YOUR_HOST/#TASK_ID` (URL-encode the task ID). No model or Instabox tokens
are sent to the browser. See [dashboard operations](docs/operations.md#dashboard).

```sh
ib-ticket link-pr fix-login --url https://github.com/YOUR_ORG/YOUR_REPO/pull/123
```

The bot records a PR only after verifying it exists and belongs to the task.
The dashboard checks the repository in the URL, but does not verify PR contents.
Polling does not resume paused VMs. Viewing an active VM can count as activity;
close the page when finished. Large events/diffs are explicitly omitted or
truncated. Logs and diffs are private work, visible to anyone with dashboard access.

After checkpoint, the latest bounded events and diff remain available as a
private saved observation. The page labels it as historical, not live.

## Optional Linear agent

Run `ib-ticket linear --settings /PRIVATE/linear.yaml` on your controller host.
Use [linear.example.yaml](linear.example.yaml) with your own workspace, agent,
team/user allowlists and secret-file paths. Each installation uses the fixed
repository/profile in your normal private config. No company settings are bundled.

Delegate an issue or mention the installed agent in Linear. Its native Agent
Session receives an acknowledgement and monitor link. Replies in that agent
conversation become durable follow-up prompts for the same issue VM/branch and
conversation. They wait for earlier work. The worker returns final agent text,
checkpoints, pauses and reports the result. Verified PR metadata is linked too.
It does not execute every ordinary issue comment or automatically publish PRs.

Setup requires a registered Linear OAuth app installed as an agent, a provisioned
agent access token and a public HTTPS webhook proxy. OAuth installation/refresh
is managed outside this binary; this is not a one-click marketplace installation.
See [Linear operations](docs/operations.md#linear-agent). The dashboard and CLI
continue to work without Linear.

## Give it to your bot

> Install this repository on your own persistent host. Read README.md and
> skills/instabox-ticket/SKILL.md. Use my private YAML and encrypted-file storage;
> inspect existing access and key provisioning before creating anything. Guide
> me through official login without asking for tokens in chat. Verify a task,
> checkpoint and revision while my laptop is offline. Return the configured
> dashboard task link and record verified PR links with link-pr. Report missing background
> execution or repository permissions. Publish a PR only within the task's scope.

Link [the skill directory](skills/instabox-ticket) into your agent's supported
skill discovery path after building the binary. For Grok installations using
`~/.grok/skills/`, link it there without replacing an existing installation.
The skill also works with other agents that can execute host commands.

[Operations](docs/operations.md) · [Security](SECURITY.md) ·
[CI](https://github.com/kevinsslin/instabox-ticket-runner/actions/workflows/test.yml)

MIT licensed. Community project, unaffiliated with Instabox or model providers.
