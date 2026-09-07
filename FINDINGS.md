# Code Review Findings — runner-only-execution vs main

Status: **simplification and reuse cleanup complete** — both groups of six candidates were verified, implemented, and checked on September 7, 2026. Reuse verification passed. Earlier simplification verification passed except for two baseline-confirmed CLI fixture failures recorded below. Altitude and efficiency candidates remain pre-verification and outside this cleanup's scope.

Scope: `git diff main...HEAD` (21 commits, ~36k insertions across 321 files).

Known intentional deferrals (per branch-deferral notes) were excluded:
unbounded peer.go request queue, credential env-scrub removal, allowed_tools
allowlist, opt-in clientCapabilities, run-scoped cleanup design, no run
concurrency ceiling, pending-affinity expiry.

## Conventions (AGENTS.md)

No violations found. AGENTS.md is the sole governing convention file; the
checked rules (pkg/errors over fmt.Errorf, testify usage, no hard-wrapped
markdown prose, sentence-case web UI copy, CLI docs updated) all pass.

## Simplification candidates (verified and implemented)

1. **Done — redundant runner guards.** `pkg/controlplane/chat.go`: removed the five repeated receiver/server nil checks after `serverChatRunner.Run`'s initial guard. Runner selection, affinity checks, and failure behavior are unchanged.
2. **Done — unreachable SDK bridge stack.** `sdk/src/agent.ts`: removed the in-process bridge/socket/RPC stack, temporary-config and private launch helpers, and `Session`'s unused bridge/config fields and cleanup. Retained public compatibility option types, existing pre-spawn rejection behavior, `Profile.toLaunchConfig()`, and the separately supported extension runtime. Added rejection and ordinary ACP lifecycle regressions.
3. **Done — unused chat helpers.** `cmd/kodelet/chat.go`: deleted `usesControlPlaneChat`, `resolveFollowConversation`, and tests/fakes used only by those helpers. Existing daemon-backed chat preparation and the production scoped `--follow` flow remain intact.
4. **Done — obsolete workspace setting and frontend branches.** `pkg/controlplane/server.go` and `pkg/webui/frontend/src`: removed `controlPlaneWorkspaceEnabled` from the settings response/type and folded its branches to the existing runner-only behavior. Removed local workspace selection and recent-local-workspace helpers; updated tests and stories to select runners explicitly. Legacy local conversations remain read-only.
5. **Done — ignored workspace-disable plumbing.** `cmd/kodelet/serve.go` and `pkg/controlplane/server.go`: removed the unused configuration fields, flag-value forwarding, forced validation value, and constant logging field. The deprecated `--disable-control-plane-workspace` flag and `serve.disable_control_plane_workspace` YAML key remain accepted and ignored; other trusted settings still receive strict validation. Added compatibility coverage for both legacy values and embedded-runner modes; documented the no-op behavior.
6. **Done — explicit UI input ownership.** `pkg/controlplane/ui_input.go`: the broker constructor now leaves `owner` nil, and chat admission no longer needs a manual reset. Tests that need an owner set one explicitly; new regression coverage confirms that unowned input, confirm, select, and notification requests emit no events and return unavailable.

### Simplification verification

- Passed: focused control-plane regressions and full race-enabled suites for `pkg/controlplane`, `pkg/chat`, and `pkg/webui`.
- Passed: focused serve/chat/command-resolution tests, including deprecated flag/YAML compatibility cases.
- Passed: `mise run sdk-test` (typecheck, build, all 96 SDK tests, package dry-run).
- Passed: `mise run frontend-test` (550 tests across 35 files), `mise run frontend-lint`, and `mise run code-generation` (TypeScript check and production Vite build).
- Passed: `mise run lint` (frontend production build, `go vet ./...`, and `golangci-lint run`, zero issues).
- Full CLI race suite: completed with two pre-existing fixture failures, not a clean pass. `TestDaemonChatRendersAndAcceptsInputBeforeBootstrapAndExtensionsReady` omits `/api/chat/message-history` from its mock handler; `TestOIDCClientsReuseSavedLoginWithoutLocalDiscovery/chat` omits `/api/chat/cwd-suggestions`. Both failed identically in all three targeted race-test runs on the current worktree and an isolated archive of baseline `HEAD` (`269149ec54654ace4d1b2fb0084c3ca0c31d5e91`). The request paths and failing test bodies are unchanged by this cleanup; no unrelated fixture fixes were made. An initial full CLI attempt exceeded a 90-second overall timeout; the completed rerun used a 10-minute timeout.

## Altitude candidates (pre-verification)

1. `cmd/kodelet/main.go:456` — per-command startup dispatch by
   string-matching `cmd.Name()=="start" && parent.Name()=="runner"` in the
   shared root PersistentPreRunE; serve.go does the same init inline.
   Belongs in runner start's RunE or a cobra annotation read generically.
2. `cmd/kodelet/run_remote.go:216` — default-runner resolution (ChatSettings
   -> DefaultRunnerReady -> RunnerID) reimplemented at four client sites
   (run_remote.go x2, chat.go:92-104, pkg/acp/remote.go:93-101) although the
   server already resolves empty RunnerID. Belongs once in pkg/chat.Client.
3. `cmd/kodelet/run_remote.go:57` — four hand-maintained flag-name lists gate
   remote forwarding (run_remote.go:57 denylist, chat.go:67 near-duplicate,
   workspace_inspection.go:53-64 allowlist, remoteRunExecutionOptions
   forwarding map). A new persistent flag is silently dropped for run/chat.
   Classify flags once via annotations at registration.
4. `cmd/kodelet/chat.go:166` — `configuredChatRunner.Run` re-downloads the
   full conversation via LoadConversation every turn and re-validates
   runner/CWD/profile affinity client-side, duplicating per-turn server
   resolution. Per-turn enforcement belongs server-side; fail-fast belongs
   in prepareDaemonChat (already present).
5. `pkg/controlplane/runner_persistent_ui.go:425` — `RunnerUIDetached`
   rebuilds composite identity strings inline ("runner:"+RunnerID, HasPrefix
   on "gen:owner") instead of using/co-locating the encode helpers; a format
   change silently breaks stale-widget cleanup.
6. `pkg/chat/chat.go:451` — child-delegation wired via context-key lookup
   plus concrete-type assertion to `*agentenv.RemoteEnvironment`
   (SetChildRunID/SetChildPrompt); any other Environment implementation
   fails at runtime. Belongs as a capability interface in pkg/agentenv.

## Reuse candidates (verified and implemented)

1. **Done — shared loopback classification.** Removed `localServerHost`; connection-state validation, automatic startup, and foreground daemon discovery now call `controlplaneurl.IsLoopbackHostname`. Added regressions for case-insensitive/trailing-dot localhost names, IPv4/IPv6 loopback, non-loopback rejection, and unchanged endpoint restrictions.
2. **Done — shared live-fork persistence.** All three providers delegate `ForkConversation` to `pkg/llm/base`, sharing snapshot error handling, live-snapshot mode, initiator propagation, and fork persistence. Provider-specific snapshot validation and locking remain with each provider.
3. **Done — shared admission checkpoint lifecycle.** All three providers delegate `SavePendingUserMessage` to `pkg/llm/base`, centralizing persistence availability checks, appending admitted input, saving, and deferred restoration. Providers retain their own state-isolation callbacks; Responses retains its operation lock, operation context, and full no-save snapshot.
4. **Done — shared workspace-target query builder.** Added `WorkspaceTarget.queryValues()` and reused it for discovery, inspection, message history, and commit queries. Discovery still adds query/options separately, inspection still rejects conversation/options targets, and commit queries still omit profile selections.
5. **Done — shared enrollment request assembly.** Added `runnerclient.NewEnrollmentStartRequest` for public-key encoding, fingerprinting, protocol/workspace metadata, hostname/version validation, and request validation. Standalone and embedded enrollment both use it, retaining caller-specific host metadata and embedded credential reuse/revocation behavior. Embedded enrollment now records `KodeletVersion`. Correction to the original finding: embedded requests were already validated indirectly by `authStore.StartRunnerEnrollment`; the missing version metadata and duplicated assembly were real, not an absence of server-side validation.
6. **Done — shared strict decoding and null-options guard.** Both request unmarshallers use the same strict JSON decoder and case-insensitive null-options detector, with the security rationale documented once. Their distinct envelope locations, validation order, error messages, and assign-only-on-success behavior remain unchanged.

### Reuse verification

- Passed: focused race-enabled persistence, fork, admission checkpoint, no-save, and restoration tests across `pkg/llm/base`, `pkg/llm/anthropic`, `pkg/llm/openai`, and `pkg/llm/openai/responses`. New regressions cover successful saves, save/cancellation errors, backing-slice isolation, Responses operation locking/context and external appends, fork initiators, and unchanged parent state.
- Passed: full race-enabled suites for `pkg/chat`, `pkg/controlplane`, `pkg/runner/client`, and `pkg/runner/controlplaneurl`.
- Passed: focused race-enabled CLI local-server lifecycle, loopback endpoint, and serve-configuration tests; focused standalone/embedded enrollment tests. Added coverage confirms embedded enrollment persists version metadata and retains credential reuse/revocation behavior.
- Passed: `mise run lint` after the regression additions (frontend TypeScript/production build, `go vet ./...`, and `golangci-lint run`, zero issues), scoped formatting, and `git diff --check`.
- Final static review found no actionable regressions. No full repository test run or full CLI suite was repeated for this cleanup; the earlier baseline-confirmed CLI fixture failures remain outside this scope.

## Efficiency candidates (pre-verification)

1. `cmd/kodelet/chat.go:168` — every message submit re-downloads and
   normalizes the FULL conversation (up to 64MB) just to read 4 immutable
   affinity fields; cache the affinity tuple after the first load.
2. `pkg/tui/update.go:591` — after every extension-shortcut execution the TUI
   re-runs full remote workspace discovery (extension-runtime cold start)
   although the execute path just probed and digest-checked; thread the
   digest through instead.
3. `pkg/tui/shortcuts.go:141` — every shortcut keystroke pays a full runner
   discovery probe purely to re-check a digest the server validates again;
   send the cached digest and let server conflicts drive reloads.
4. `cmd/kodelet/chat.go:311` — chat startup fetches /api/chat/settings again
   inside discoveryTarget right after prepareRemoteChatSettings fetched it
   (N+2 sequential round-trips with N profiles); seed the values from the
   first response.
5. `pkg/controlplane/turn_store.go:116` — chat_turns rows (with full result
   text) are inserted per turn and never deleted, even on conversation
   delete; unbounded SQLite growth on a long-lived daemon.
6. `pkg/controlplane/extensions_ui.go:52` — webExtensionUIHost.closed map
   gains an entry per closed extension generation and is never evicted;
   grows without bound with flapping runners. Store highest closed
   generation per key instead.
