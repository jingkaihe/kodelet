---
name: self-test
description: Build and test Kodelet changes with an isolated server, CLI, and shared browser.
---

# Self-test Kodelet

Validate a candidate while the current server keeps hosting this conversation. Follow AGENTS.md; choose assertions specific to the change, not just successful startup.

## Safety

- Never install the candidate, stop the live server, bypass its lock, or use its database. Preserve unrelated worktree changes.
- Use fresh state, a temporary HOME, and a disposable workspace. KODELET_BASE_PATH alone does not relocate every file; neither mechanism is a filesystem/network sandbox.
- Keep real-provider calls bounded: they consume quota. Never print credentials or the full user config. Inspect receipts/logs before retrying interrupted work.

## 1. Build and prepare

Run from the repo root using the normal home/toolchain. Use the tmux skill for long-running commands.

```bash
git status --short
mise run build
./bin/kodelet version
REPO_ROOT="$PWD"
SOURCE_HOME="$HOME"
LIVE_BASE="${KODELET_BASE_PATH:-$SOURCE_HOME/.kodelet}"
SELF_TEST_ROOT=$(mktemp -d /tmp/kodelet-self-test.XXXXXX)
chmod 700 "$SELF_TEST_ROOT"
mkdir -p "$SELF_TEST_ROOT/home/.kodelet/bin" "$SELF_TEST_ROOT/state" "$SELF_TEST_ROOT/workspace"
printf 'Trial directory: %s\n' "$SELF_TEST_ROOT"
```

Restore these variables in subsequent tool calls; do not globally export the temporary HOME. Record the live PID/instance/URL from `$LIVE_BASE/server/connection.json` and checksums of the source config and installed binary.

Create `$SELF_TEST_ROOT/config.yaml` (0600) using a YAML parser, preserving `profile`, `profiles`, and relevant `tool_mode`/`code_mode` settings from `~/.kodelet/config.yaml`. Do not hard-code model names. Replace server settings:

```yaml
serve:
  host: 127.0.0.1
  port: 0
  web_auth_mode: token
  runner_auth_mode: token
  embedded_runner: true
  runner_workspace: /absolute/trial/workspace
```

Omit OIDC, public URLs, existing server tokens, and browser settings. Disable extensions, skills, and tracing; use `serve.runner_settings` to override inherited runner integrations. Enable extras only when required by the test.

Copy only required provider credentials and referenced files (especially custom `sysprompt`) into the temporary home with private permissions. Rewrite absolute paths if necessary; never symlink mutable state to the live home. Existing managed rg/fd binaries may be copied to avoid downloads.

For Codex, check `.tokens.expires_at` in `~/.kodelet/codex-credentials.json` exceeds `now + 3600` before copying. If not, resolve authentication first: copied refresh tokens can rotate the live grant. Other providers need their own credential checks. Inherited API keys are usable; never copy candidate credentials back.

## 2. Start the candidate

```bash
candidate() {
  env HOME="$SELF_TEST_ROOT/home" \
    KODELET_BASE_PATH="$SELF_TEST_ROOT/state" \
    KODELET_CONFIG_FILE="$SELF_TEST_ROOT/config.yaml" \
    KODELET_CONFIG_FILE_MODE=isolated \
    "$REPO_ROOT/bin/kodelet" "$@"
}
candidate server start
candidate server status
```

Use `state/server/connection.json` and `state/server/client-token` beneath the trial directory for discovery/authentication. Assert API/runner readiness, a different PID/instance from live, and the expected commit/build time at `/api/status`. Logs are in `state/server/server.log`; never fall back to the live endpoint.

## 3. Validate behavior

- **CLI/tools:** Pass explicit candidate `--server`, `--auth-token`, and disposable `--cwd`. Write a random marker to `fixture.txt`; ask the model to read it without revealing it in the prompt. Use `--allowed-tools code_execute,bash --allowed-commands "cat fixture.txt" --max-turns 6 --result-only`. Assert exact output and a saved successful tool result.
- **Code mode:** Keep `code_execute` in the allowlist; allowing only `bash` disables code mode. Verify actual code-execution calls and nested results.
- **Persistence:** Restart only the idle candidate, rediscover its URL/token, and resume with `--no-tools`. Assert it recalls the marker.
- **Ownership:** When relevant, a second `candidate serve` against candidate state must fail with the ownership error.
- **Regression:** Add an assertion that would fail without the change. Smoke tests complement targeted repository tests, not replace them.

## 4. Shared-browser Web UI test

Use the current conversation’s browser tool; the candidate does not need browser integration enabled.

1. Get `candidate server url`, then use browser actions `open` and `navigate`. Keep the token URL out of reports; inspect DOM readiness with `evaluate`.
2. Inspect current controls, select a model, enter a prompt, and click Send. For React textareas, use the native value setter and a bubbling input event; check Send is enabled in a subsequent evaluation.
3. Observe execution and the final response; expand code/tool details and verify command, exit status, output, and fixture marker. Do not substitute direct API calls for UI interactions.
4. Reload the saved `/c/<id>` route, confirm history, and send a follow-up through the composer. Check persisted turn outcomes and candidate logs.
5. Capture a screenshot artifact. Error/unhandledrejection listeners help, but are not complete console coverage. The user shares this page: reinspect unexpected changes.

## 5. Clean up and report

Confirm the live instance is unchanged and ready, and source config/installed-binary checksums match. When checking data isolation, query candidate IDs in the live DB read-only without exposing history.

Run `candidate server stop` after work finishes; verify the process exited before removing the exact trial directory, including copied credentials, helpers, and fixtures. Cancel failed candidate work first if necessary. Clean up only trial tmux sessions; retain screenshot artifacts and `./bin/kodelet`.

Leave the shared browser page open, but stop the candidate unless a running preview was requested; explain it is no longer interactive. Report build identity, actual checks, failures/limitations, isolation, and cleanup—never claim exhaustive coverage from smoke tests.
