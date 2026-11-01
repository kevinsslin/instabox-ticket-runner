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
ib-ticket checkpoint fix-login
```

Use the VM and conversation IDs returned by the controller. Follow events until
the turn finishes, inspect the diff and tests, then checkpoint. Request revisions
with another `run` for the same task ID. A successful start is not task completion.

The bot needs a durable background job to monitor events and checkpoint after
your phone disconnects. This CLI does not provide a supervisor or queue. One
Codex profile executes serially on one trusted controller host.

## Give it to your bot

> Install this repository on your own persistent host. Read README.md and
> skills/instabox-ticket/SKILL.md. Use my private YAML and encrypted-file storage;
> inspect existing access and key provisioning before creating anything. Guide
> me through official login without asking for tokens in chat. Verify a task,
> checkpoint and revision while my laptop is offline. Report missing background
> execution or repository permissions. Publish a PR only within the task's scope.

Link [the skill directory](skills/instabox-ticket) into your agent's supported
skill discovery path after building the binary. For Grok installations using
`~/.grok/skills/`, link it there without replacing an existing installation.
The skill also works with other agents that can execute host commands.

[Operations](docs/operations.md) · [Security](SECURITY.md) ·
[CI](https://github.com/kevinsslin/instabox-ticket-runner/actions/workflows/test.yml)

MIT licensed. Community project, unaffiliated with Instabox or model providers.
