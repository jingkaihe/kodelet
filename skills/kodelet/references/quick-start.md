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

Start `kodelet serve` separately and supply its web/API token through `KODELET_AUTH_TOKEN`. Execution always uses the daemon; choose its endpoint with `--server`, `KODELET_SERVER`, or a trusted user-level `server` setting:

```bash
kodelet run --server https://kodelet.example --runner project-runner --cwd /workspace/project "inspect the repository"
kodelet run --server https://kodelet.example --runner project-runner --no-tools --result-only "explain the context"
kodelet run --server https://kodelet.example --resume CONVERSATION_ID "continue"
```

Runs always save; `--no-save` is removed. The daemon owns provider credentials and history; runners own workspaces and tools. The recognized same-host default uses the invoking CWD for new CLI conversations; other targets use runner-host path rules. Resumes retain stored affinity. Ctrl+C requests an exact turn stop; disconnects only detach. Inspect `kodelet conversation turn <conversation-id> <turn-id>` before resubmitting uncertain work. Client authentication uses `kodelet auth login` or a web/API token, never a runner token. There is no local execution fallback.

### Interactive/IDE mode (ACP)

ACP requires a running daemon and a registered runner:

```bash
kodelet acp
kodelet acp --server https://kodelet.example --runner workstation
```

Use `server` or `KODELET_SERVER` for the default endpoint. For OIDC, run `kodelet auth login --server https://kodelet.example`; `--auth-token` overrides `KODELET_AUTH_TOKEN` and stored login state. ACP uses client credentials only, never enrolls or starts a runner, and has no local execution fallback. Without `--runner`, new sessions use the daemon's default runner.

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

Chat always uses the daemon, including the default local endpoint. Directories are runner-side; resume keeps stored affinity and `--follow` needs `--runner` or `--cwd`. Exit detaches, `/stop` cancels, and `/take-control` requests ownership of future prompts without replaying dismissed prompts.

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
