# Kodelet quick start

## Installation

```bash
# Package-based install by default where available
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash

# Force standalone binary install
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash -s -- --binary
```

Show version and build info:

```bash
kodelet version
```

## Core usage modes

### One-shot mode

```bash
kodelet run "your query"
kodelet run --cwd "$PWD" -f "continue the task" # scoped --follow
kodelet run --resume CONVERSATION_ID "more questions"
kodelet run --result-only "what is 2+2"
kodelet run --no-tools "what is the capital of France?"
```

Local chat/run/ACP commands automatically start a detached server when needed and discover its credential; no separate terminal or token copying is required. The server remains running after the client exits. Execution always uses the daemon. An explicit `--server`, `KODELET_SERVER`, or trusted user-level `server` setting is connect-only and never falls back to a local server:

```bash
kodelet run --server https://kodelet.example --runner project-runner --cwd /workspace/project "inspect the repository"
kodelet run --server https://kodelet.example --runner project-runner --no-tools --result-only "explain the context"
kodelet run --server https://kodelet.example --resume CONVERSATION_ID "continue"
```

Conversations are saved automatically; omit the removed `--no-save` flag. The server stores provider credentials and conversation history, while runners provide file access and tools. New conversations using this machine's built-in runner start in your current directory. For other runners, directories refer to paths on the runner's machine. Resuming keeps the saved runner and directory. Ctrl+C requests cancellation; losing the connection does not stop the work. If a request is interrupted, check `kodelet conversation turn <conversation-id> <turn-id>` before sending it again. Sign in with `kodelet auth login` or supply a web/API token; runner tokens are for connecting runners, not CLI clients.

### Interactive/IDE mode (ACP)

ACP starts the local daemon and embedded runner automatically when needed:

```bash
kodelet acp
kodelet acp --server https://kodelet.example --runner workstation
```

Use `server` or `KODELET_SERVER` to select an explicitly managed server address. For OIDC, run `kodelet auth login --server https://kodelet.example`; `--auth-token` overrides `KODELET_AUTH_TOKEN` and saved sign-in credentials. Explicitly selected servers and separately managed runners must already be running. Without `--runner`, new sessions use the server's default runner. Automatic startup writes diagnostics only to stderr.

Example Zed-style configuration:

```json
{
  "agent": {
    "command": "kodelet",
    "args": ["acp"]
  }
}
```

ACP supports persisted history, images, streaming, tool visualization, and runner-backed slash-command discovery. Session directories are validated on the runner, independently of the client's startup directory. Resume preserves stored runner/CWD/profile affinity. Closing the client detaches; an explicit cancel action stops only its active prompt. Model and restriction flags use the daemon execution contract; configure extension installations and provider credentials on their owning hosts.

### Terminal chat TUI

```bash
kodelet chat
kodelet chat --profile openai --reasoning-effort high
kodelet chat --resume CONVERSATION_ID
kodelet chat --theme catppuccin-latte
kodelet chat --runner project-runner --cwd ../another-project --server https://kodelet.example
```

Chat starts or reuses the local server automatically unless a server is explicitly selected. Directories refer to paths on the runner's machine. Resuming keeps the saved runner, directory, and profiles; use `--follow` with `--runner` or `--cwd` to choose which history to search. Exiting chat leaves work running; `/stop` cancels it. Use `/take-control` to receive future interactive prompts in this client. Previously dismissed prompts are not shown again.

### Local server lifecycle

```bash
kodelet server start
kodelet server status
kodelet server logs
kodelet server stop             # refuses while agent runs are active
kodelet server restart         # reload trusted configuration after changes
kodelet server stop --force    # cancel active runs and stop
```

Connection state and the local API credential live under `~/.kodelet/server/` (or `$KODELET_BASE_PATH/server/`), separately from user-edited configuration. The directory is private (`0700`) and files are owner-only (`0600`). Managed startup requires loopback token authentication and an enabled embedded runner; public/OIDC or external-runner-only deployments use explicit `serve` and `--server`. The default port is 8080; `serve.port: 0` selects an available port and publishes it for discovery. The managed runner defaults to the home directory for stable identity, while new same-host conversations use the client's current directory. Trusted configuration/environment is inherited at startup and remains pinned until restart. Foreground `kodelet serve` remains available and operator-owned. Detachment is not a reboot/login service or automatic crash supervisor.

### Web UI

```bash
kodelet serve
kodelet serve --host 0.0.0.0 --port 3000
kodelet serve --cors-origins https://app.example.com,https://admin.example.com
kodelet serve --embedded-runner=false
```

`serve` hosts an embedded runner by default; `--embedded-runner=false` uses external runners only. Workspace execution, discovery, Git and terminals always stay on runners. Use `--runner-workspace` instead of the removed `serve --cwd`.

With no explicit authentication modes, `kodelet serve` prints separate generated web/API and runner tokens. Tokens supplied through flags or trusted configuration are not echoed. For native browser OIDC and browser-approved runners, create a Web application OAuth client with the exact callback URI, store its client secret in a regular owner-only file, and run:

```bash
kodelet serve \
  --web-auth-mode oidc \
  --oidc-issuer https://accounts.google.com \
  --oidc-client-id CLIENT_ID.apps.googleusercontent.com \
  --oidc-client-secret-file "$HOME/.kodelet/google-oidc-client-secret" \
  --oidc-redirect-url https://kodelet.example/auth/oidc/callback \
  --oidc-allowed-domains example.com \
  --oidc-admin-emails admin@example.com \
  --oidc-runner-admin-emails runners@example.com \
  --runner-auth-mode enrollment
```

Every accepted OIDC identity receives normal shared chat access. `terminal`, `runner-admin`, and `admin` roles gate the server-host terminal and runner administration; this remains a shared server rather than per-user tenant isolation. Enrollment mode requires at least one runner-admin/admin email unless an administrative compatibility token will approve runners. Pure OIDC does not generate an administrative compatibility token; CLI, TUI, and ACP users authenticate with `kodelet auth login`, while trusted `serve.auth_token`, `KODELET_AUTH_TOKEN`, and `--auth-token` remain migration or automation overrides.

Instead of repeating those flags, put the corresponding values under `serve:` in `~/.kodelet/config.yaml` or an explicitly selected `KODELET_CONFIG_FILE`; explicit flags override YAML. Repository-level `kodelet-config.yaml` cannot set `serve`. The nested OIDC keys are `issuer`, `client_id`, `client_secret_file`, `redirect_url`, `scopes`, `allowed_emails`, `allowed_domains`, `admin_emails`, `terminal_emails`, `runner_admin_emails`, `allow_any_user`, and `session_duration`.

Enroll and start a workspace runner with:

```bash
cd ~/src/project
kodelet runner enroll --server https://kodelet.example --name project-runner
kodelet runner start --server https://kodelet.example
```

Enrollment opens a browser approval flow and stores a workspace-bound opaque access token and Ed25519 private key outside the repository. Each runner WebSocket connection sends a fresh RFC 9449 DPoP proof bound to the token, request method, and target URL; replayed proofs are rejected. A `--replace` enrollment can revoke and disconnect an existing enrolled generation after separate browser confirmation. Token and enrollment authentication are mutually exclusive runner modes; remove any explicit runner token when using enrollment mode. Loopback CORS origins are allowed by default; use `--cors-origins` for additional browser origins.

## Project context

Kodelet automatically loads `AGENTS.md` from the current repository. Good context files include project structure, tech stack, build/test/lint commands, coding style, and deployment notes.

Bootstrap one:

```bash
kodelet run -r init
```

## Git helpers

```bash
git add .

# Fast, non-interactive commit message generation
kodelet commit --no-confirm

# Include a ticket prefix
kodelet commit --prefix TICKET-123

# Interactive commit message flow
kodelet commit

# Pull requests
kodelet pr
kodelet pr --target main
kodelet pr --draft
```

## Image input

```bash
kodelet run --image /path/to/screenshot.png "What's wrong with this UI?"
kodelet run --image ./diagram.png --image ./mockup.jpg "Compare these designs"
```

Supported formats: JPEG, PNG, GIF, WebP. Limits: 5 MB per image, 10 images per message. Provider/model must support multimodal input.

## Shell completion

```bash
# Bash
echo 'source <(kodelet completion bash)' >> ~/.bashrc

# Zsh
echo 'source <(kodelet completion zsh)' >> ~/.zshrc

# Fish
kodelet completion fish > ~/.config/fish/completions/kodelet.fish
```

## Common workflows

```bash
# Review changes
git diff main | kodelet run "review these changes for issues"

# Investigate and then implement
kodelet run "analyze error logs and suggest fixes"
kodelet run -f "implement the suggested fix" # same as --follow

# Refactor or test
kodelet run "refactor user authentication to use middleware pattern"
kodelet run "write unit tests for the payment processing module"
```
