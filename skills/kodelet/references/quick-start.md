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
kodelet run -f "continue the task" # same as --follow
kodelet run --resume CONVERSATION_ID "more questions"
kodelet run --no-save "temporary query"
kodelet run --result-only "what is 2+2"
kodelet run --no-tools "what is the capital of France?"
```

### Interactive/IDE mode (ACP)

Kodelet implements the Agent Client Protocol (ACP):

```bash
kodelet acp
```

To use a remote control-plane agentic loop while retaining local workspace tools, context, skills, recipes, and extensions:

```bash
kodelet acp --server https://kodelet.example
```

For an OIDC control plane, run `kodelet auth login --server https://kodelet.example`; ACP automatically uses the resulting Kodelet-issued per-server credential. An explicit `--auth-token` takes precedence over `KODELET_AUTH_TOKEN`, which takes precedence over stored login state. In runner enrollment mode, ACP separately uses the current workspace's stored DPoP credential when no explicit `--runner-auth-token` or `KODELET_RUNNER_AUTH_TOKEN` is supplied; enroll first with `kodelet runner enroll --server https://kodelet.example`. An explicit runner token takes precedence for legacy migration. If a same-server `kodelet runner start` process already owns the current workspace, ACP reuses that runner; different ACP sessions can run concurrently through it.

Example Zed-style configuration:

```json
{
  "agent": {
    "command": "kodelet",
    "args": ["acp"]
  }
}
```

ACP supports session persistence, image input, embedded file context, streaming responses, tool-call visualization, and local slash-command discovery in compatible clients. Server-backed ACP persists conversations on the control plane and resumes only conversations bound to the selected workspace runner.

### Terminal chat TUI

```bash
kodelet chat
kodelet chat --profile openai --reasoning-effort high
kodelet chat --resume CONVERSATION_ID
kodelet chat --theme catppuccin-latte
```

The default `auto` theme selects Catppuccin Latte for light terminal profiles and Catppuccin Mocha for dark terminal profiles. Use `--theme` at startup or `/theme` in the TUI; custom `*.theme` files belong in `~/.kodelet/themes`. Before sending the first message, use `Ctrl+T` to change profile and `Ctrl+Y` or the clickable `effort:` label to choose an allowed reasoning effort. Persisted conversations restore and lock their configuration snapshot on resume.

### Web UI

```bash
kodelet serve
kodelet serve --host 0.0.0.0 --port 3000
kodelet serve --cors-origins https://app.example.com,https://admin.example.com
```

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

Enrollment opens a browser approval flow and stores a workspace-bound opaque access token and Ed25519 private key outside the repository. Each runner WebSocket connection sends a fresh RFC 9449 DPoP proof bound to the token, request method, and target URL; replayed proofs are rejected. A `--replace` enrollment can revoke and disconnect an existing enrolled generation after separate browser confirmation. Use runner `hybrid` mode during migration from the legacy shared token; after an identity is enrolled, shared-token registration for that host/workspace is rejected. Loopback CORS origins are allowed by default; use `--cors-origins` for additional browser origins.

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
