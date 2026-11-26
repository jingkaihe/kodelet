# Schedule extension

`kodelet-extension-schedule` runs agent tasks on recurring schedules. Each run starts a new agent conversation, so you can open, resume, or fork it like any other conversation.

One executable has two modes that share a SQLite store:

- With no arguments, Kodelet starts it as an extension that gives the agent tools to manage the schedules of the current workspace.
- `kodelet-extension-schedule daemon` is a long-running process that you supervise. It starts due runs through `kodelet acp`.

## Install

```bash
mise run schedule-build
mkdir -p ~/.kodelet/extensions
cp bin/kodelet-extension-schedule ~/.kodelet/extensions/
```

Any [extension root](../../skills/kodelet/references/extensions.md#discovery) works, including a directory on the runner's `PATH`. Kodelet discovers the extension in new sessions; use `/reload` in a running conversation.

## Tools

| Tool | Purpose |
| --- | --- |
| `schedule_list` | List this workspace's schedules with their next and latest runs |
| `schedule_create` | Create a schedule |
| `schedule_update` | Change a schedule by ID; omitted fields keep their values |
| `schedule_delete` | Delete a schedule and its run history |

A schedule has a `name` (unique in its workspace), a `schedule`, a `task` with the instructions for each run, a `cwd`, and an `enabled` flag. The agent cannot choose where or how a schedule runs:

- The workspace is the runner hosting the conversation that creates the schedule. Tools only see and change schedules of that runner.
- `cwd` defaults to the conversation's directory and must stay inside it.
- The model profile is the conversation's profile, including profiles provided by extensions. Updates never change it; to use another profile, recreate the schedule from a conversation using that profile. Kodelet resolves the profile again on the schedule's runner and directory at the start of every run.

Creating, updating, and deleting a schedule needs the user's approval in an interactive client. Without one, for example in `kodelet run` or a scheduled run, these tools refuse, so unattended agents cannot plant or alter schedules.

### Schedule syntax

Schedules use five-field cron expressions (`minute hour day-of-month month day-of-week`) or descriptors such as `@hourly`, `@daily`, `@weekly`, and `@every 6h`. Prefix `CRON_TZ=<IANA zone>` to pick a timezone, for example `CRON_TZ=Europe/London 0 9 * * 1-5`; otherwise the host's local time applies. Schedules may fire at most once a minute.

## Run the daemon

```bash
kodelet-extension-schedule daemon
```

The daemon checks for due schedules, starts each run with `kodelet acp --runner <workspace>` in the schedule's directory and profile, and records its conversation and outcome. It reaches the server the same way the `kodelet` CLI does, so a local server starts automatically, and remote servers need `server` configuration and `kodelet auth login`.

| Flag | Default | Purpose |
| --- | --- | --- |
| `--db` | see [storage](#storage) | Schedule database |
| `--server` | CLI configuration | Kodelet server URL |
| `--kodelet` | `kodelet` | `kodelet` executable used for runs |
| `--poll-interval` | `30s` | How often to check for due schedules |
| `--concurrency` | `2` | Maximum number of runs at once |
| `--run-timeout` | `1h` | Cancel runs that take longer; `0` disables |
| `--misfire-grace` | `1h` | Skip runs missed by more than this |
| `--shutdown-grace` | `30s` | Time for runs to finish on shutdown before they are cancelled |

Each occurrence runs at most once:

- A run missed while the daemon was stopped or the host slept still starts if it is within `--misfire-grace`. Older runs are recorded as skipped, and the schedule continues from the current time without catching up.
- If a schedule's previous run is still going, the new occurrence is skipped.
- At the concurrency limit, due runs wait for a free slot.
- On `SIGINT` or `SIGTERM`, the daemon stops starting runs, waits up to `--shutdown-grace`, then cancels the rest.

Only one daemon can use a database at a time. The daemon records a heartbeat, and `schedule_list` warns when it is not running.

### Keep it running

Kodelet does not supervise the daemon. On macOS, use a launchd agent such as `~/Library/LaunchAgents/dev.kodelet.schedule.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.kodelet.schedule</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/you/.kodelet/extensions/kodelet-extension-schedule</string>
    <string>daemon</string>
    <string>--kodelet</string>
    <string>/opt/homebrew/bin/kodelet</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardErrorPath</key>
  <string>/Users/you/.kodelet/logs/schedule-daemon.log</string>
</dict>
</plist>
```

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.kodelet.schedule.plist
```

On Linux, use a systemd user unit such as `~/.config/systemd/user/kodelet-schedule.service`:

```ini
[Unit]
Description=Kodelet schedule daemon

[Service]
ExecStart=%h/.kodelet/extensions/kodelet-extension-schedule daemon
Restart=on-failure

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now kodelet-schedule
```

Service managers start with a minimal environment. If the daemon starts the local Kodelet server, the server inherits that environment, so either start the server yourself first or add the provider credentials it needs to the service definition.

## Storage

Schedules, the latest 20 runs of each schedule, and the daemon heartbeat live in `$KODELET_SCHEDULE_DB`, or `schedule/schedule.db` under `$KODELET_BASE_PATH` (default `~/.kodelet`). The extension and daemon must resolve the same path.

The extension runs on the runner's host, so schedules are stored on that host and the daemon must run there too. For a remote runner, run the daemon on the runner's host with CLI access to the server.

List the schedules of every workspace on this host:

```bash
kodelet-extension-schedule list
kodelet-extension-schedule list --json
```
