# Kodelet configuration

Configure provider/model settings on the daemon, workspace/tool settings on the runner, and endpoint/display settings on the client. Trusted process settings load defaults, `~/.kodelet/config.yaml`, then an explicit `KODELET_CONFIG_FILE`; supported environment variables and flags override them. `KODELET_CONFIG_FILE_MODE=isolated` omits the global file. Client model configuration is not uploaded.

Runners independently resolve defaults → execution-CWD `kodelet-config.yaml` → trusted environment profile → permitted request restrictions. Repository settings cannot configure daemon credentials/models/endpoints, introduce trusted environment profiles, or widen host permissions. Isolated process configuration does not bypass repository policy. Workspace edits affect later runs; owner defaults require restart. See `config.sample.yaml` for the complete schema.

## Control-plane server

Configure a default `kodelet serve` control plane once for terminal chat, ACP, and runner commands in `~/.kodelet/config.yaml` or a file explicitly selected with `KODELET_CONFIG_FILE`:

```yaml
server: https://kodelet.example
```

All ordinary commands use the daemon, including `run`, chat, and ACP. Endpoint precedence is `--server`, then `KODELET_SERVER`, trusted client configuration, then `http://localhost:8080`. An unavailable daemon fails explicitly without local execution fallback. Repository settings cannot redirect clients.

The listener and authentication policy of the control plane can be configured either with explicit `kodelet serve` flags or with a top-level `serve` block in `~/.kodelet/config.yaml` or an explicitly selected `KODELET_CONFIG_FILE`. Repository-level `kodelet-config.yaml` cannot set `serve`, preventing a checked-out project from changing listener addresses, tokens, OIDC issuer/client settings, role allowlists, or runner enrollment policy. Explicit CLI flags override the corresponding trusted YAML values; serve and OIDC policy is not read from `KODELET_SERVE_*` environment variables. Web modes are `token`, `oidc`, and `none`; runner modes are `token`, `enrollment`, and `none`.

```yaml
serve:
  embedded_runner: false
  web_auth_mode: oidc
  runner_auth_mode: enrollment
  oidc:
    issuer: https://accounts.google.com
    client_id: CLIENT_ID.apps.googleusercontent.com
    client_secret_file: /run/secrets/kodelet-oidc
    redirect_url: https://kodelet.example/auth/oidc/callback
    allowed_domains: [example.com]
    admin_emails: [admin@example.com]
    runner_admin_emails: [runners@example.com]
```

`serve` enables an embedded runner by default; set `serve.embedded_runner: false` or pass `--embedded-runner=false` for external runners only. Workspace execution, discovery, Git and terminals always use runners. Use `serve.runner_workspace` instead of the removed `serve.cwd`; provider requests, history and runner coordination remain daemon-owned.

The OIDC client secret is read from a regular, non-empty, user-only referenced file, not accepted directly as a YAML value or secret-valued CLI flag. On Unix, trusted configuration containing `serve.auth_token` or `serve.runner_auth_token` must likewise be inaccessible to group and other users, such as mode `0600`; configured token values are not echoed at startup. An unreadable or malformed explicitly selected configuration file and an invalid `KODELET_CONFIG_FILE_MODE` fail closed.

In OIDC mode, browser users authenticate through server-side sessions. Non-browser clients run `kodelet auth login --server https://kodelet.example`, approve the request in an OIDC-authenticated browser, and store the resulting Kodelet-issued credential in user-only state keyed by canonical server URL. `kodelet chat --server`, `kodelet acp --server`, and runner-administration commands discover it automatically. Explicit `--auth-token` values override `KODELET_AUTH_TOKEN`, which overrides stored login state; static tokens remain administrative migration or automation credentials, and pure OIDC mode does not generate one automatically.

Saved CLI sign-ins automatically refresh one-hour access tokens without extending the original 14-day hard deadline (`serve.oidc.cli_session_duration`, default `336h`, or `--oidc-cli-session-duration`). Browser sessions use `serve.oidc.session_duration` (default `12h`); local-daemon and explicit tokens do not use refresh.

Runner enrollment state is also outside project configuration. Run `kodelet runner enroll --server https://kodelet.example` from the workspace; Kodelet stores the pending enrollment, opaque access token, private key, credential identifier, and stable registration in user-only runner state. `kodelet runner start` loads that DPoP credential automatically when no explicit runner token is supplied. ACP uses client authentication, never runner credentials. Runner tokens apply only in token mode; remove token overrides when using enrollment mode.

## Provider setup

### Anthropic Claude

```bash
# OAuth-style login flow, no API key env var needed
kodelet anthropic-login

# Or use an API key
export ANTHROPIC_API_KEY="sk-ant-api..."

kodelet run --profile anthropic "query"
```

Common model aliases in examples include `sonnet-46`, `haiku-45`, `opus-48`, and `opus-5`. Check current config/source for the latest alias mapping.

### OpenAI

```bash
export OPENAI_API_KEY="sk-..."
kodelet run --profile openai "query"
```

OpenAI supports reasoning effort values such as `none`, `minimal`, `low`, `medium`, `high`, and `xhigh` when supported by the selected model/API mode.

OpenAI text verbosity applies to the Responses API. Kodelet omits the field unless it is explicitly configured, so the upstream default applies (`medium` on OpenAI). Chat Completions requests do not send this setting. Set `openai.text_verbosity` to `low`, `medium`, or `high` inside the model profile:

```yaml
profiles:
  openai:
    provider: openai
    model: gpt-6-astra
    openai:
      api_mode: responses
      text_verbosity: high
```

## Example config

```yaml
aliases:
  gpt-6: gpt-6-astra
  haiku-45: claude-haiku-4-5-20251001
  opus-48: claude-opus-4-8
  opus-5: claude-opus-5
  sonnet-46: claude-sonnet-4-6

profile: anthropic

# Shared settings remain top-level and are retained for every profile.
extensions:
  enabled: true
tracing:
  enabled: false

profiles:
  anthropic:
    provider: anthropic
    model: sonnet-46
    weak_model: haiku-45
    max_tokens: 16000
    reasoning_effort: medium
    allowed_reasoning_efforts: [low, medium, high]
    # Optional: force adaptive-thinking plumbing for custom Anthropic model IDs.
    # anthropic:
    #   adaptive_thinking: true

  openai:
    provider: openai
    model: gpt-6-astra
    weak_model: gpt-5
    reasoning_effort: medium
    allowed_reasoning_efforts: [low, medium, high]
    tool_mode: patch
    enable_fs_search_tools: false

  codex:
    provider: openai
    model: gpt-6-astra
    weak_model: gpt-5.6-luna
    reasoning_effort: xhigh
    allowed_reasoning_efforts: [low, medium, high, xhigh, max]
    tool_mode: patch
    enable_fs_search_tools: false
    openai:
      platform: codex
      api_mode: responses

```

Named profiles are the only model configuration mechanism. The daemon requires `profile: <name>` to select its default and an explicit `provider` and `model` in every profile. Omitting `--profile` selects that configured name; an explicit name selects that profile. Missing selectors and unknown names are errors. There is no unnamed model fallback or reserved `default` model profile; `default` works only if a profile with that name is defined.

Model-valued environment variables such as `KODELET_MODEL`, `KODELET_PROVIDER`, `KODELET_WEAK_MODEL`, `KODELET_MAX_TOKENS`, `KODELET_REASONING_EFFORT`, and `KODELET_OPENAI_API_MODE` are removed and rejected at daemon startup. Define their values inside profiles. `KODELET_PROFILE` remains supported, along with provider credential variables and supported explicit CLI model overrides.

Model fields, including weak-model settings, token limits, reasoning settings, and model-specific provider options, belong inside profiles, not at the top level. Optional model fields use built-in field defaults. Shared non-model settings such as extensions, skills, tracing, tools, aliases, and connection/authentication settings remain top-level. Supported profile overrides deep-merge nested maps, replace scalar and list values, and preserve omitted shared fields. This does not change daemon/runner ownership or make process-startup settings dynamically switchable.

Pickers display plain profile names and indicate the current selection without a default label. Changing `profile:` does not change an explicitly selected profile's model settings. Restart the daemon after changing its configuration; existing conversations retain their saved model configuration. `kodelet setup` generates named `openai` and `anthropic` profiles and selects `openai`.

`allowed_reasoning_efforts` defines the ordered reasoning-effort choices available for new conversations in the TUI and Web UI. When omitted or empty, all efforts supported by the configured provider are available.

Profile definitions support `hidden: true` to omit them from normal pickers without preventing explicit selection. Extensions can [register self-contained profiles](sdk.md#registered-model-profiles) using ordinary configuration keys and built-in defaults, without editing daemon configuration.

## Skills config

```yaml
skills:
  enabled: true
  allowed:
    - pdf
    - xlsx
```

Disable for one run:

```bash
kodelet run --no-skills "query"
kodelet acp --no-skills
```

## Extension config

See `references/extensions.md` for the full extension model. Minimal config:

```yaml
extensions:
  enabled: true
  global_dir: ~/.kodelet/extensions
  local_dir: ./.kodelet/extensions
  max_output_size: 102400
```

Disable for one run:

```bash
kodelet run --no-extensions "query"
kodelet acp --no-extensions
```

## Command and tool restrictions

Restrict bash commands:

```yaml
allowed_commands:
  - "ls *"
  - "pwd"
  - "git status"
  - "npm *"
```

Or with an environment variable:

```bash
export KODELET_ALLOWED_COMMANDS="ls *,pwd,git status"
```

Set the maximum timeout the bash tool can request. Default is `120s`:

```yaml
bash:
  timeout: 5m
```

Or:

```bash
export KODELET_BASH_TIMEOUT=5m
```

Restrict model tools for a run:

```bash
kodelet run --allowed-tools "file_read,grep_tool,bash" "analyze code"
```
