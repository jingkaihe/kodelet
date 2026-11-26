# Schedule extension

`kodelet-extension-schedule` runs agent instructions on recurring schedules. Each run starts a new agent conversation, so you can open, resume, or fork it like any other conversation.

One executable has two modes that share a SQLite store:

- With no arguments, Kodelet starts it as an extension that gives the agent tools to manage the schedules of the current workspace.
- `kodelet-extension-schedule daemon` is a long-running process that you supervise. It starts due runs through `kodelet acp`.

## Install

```bash
mise run schedule-install
```

This builds `bin/kodelet-extension-schedule` and installs it in `~/.local/bin`, so the same executable serves as the extension and the `kodelet-extension-schedule daemon` command; set `SCHEDULE_INSTALL_DIR` to install elsewhere. It replaces the file by renaming, so a running daemon is unaffected until you restart it.

Kodelet discovers extensions on the runner's `PATH` unless `extensions.discover_path` is `false`, so the directory must be on `PATH` where the Kodelet server starts; restart the local server with `kodelet server restart` if it started before `~/.local/bin` was on its `PATH`. A copy in an [extension root](../../skills/kodelet/references/extensions.md#discovery) such as `~/.kodelet/extensions` takes precedence over `PATH`. Kodelet discovers the extension in new sessions; use `/reload` in a running conversation.

## Tools

| Tool | Purpose |
| --- | --- |
| `schedule_list` | List this workspace's schedules with their next and latest runs |
| `schedule_create` | Create a schedule |
| `schedule_update` | Change a schedule by ID; omitted fields keep their values |
| `schedule_delete` | Delete a schedule and its run history |
| `schedule_history` | List recent runs, newest first; filter by `scheduleId` or `status`, and set `limit` (1 to 50, default 10) |
| `schedule_run_details` | Show a run's instruction, timing, stats, and final output |

A schedule has a `name` (unique in its workspace), a `schedule`, an `instruction` for each run, a `cwd`, and an `enabled` flag. The agent cannot choose where or how a schedule runs:

- The workspace is the runner hosting the conversation that creates the schedule. Tools only see and change schedules of that runner.
- `cwd` defaults to the conversation's directory and must stay inside it.
- The model profile is the conversation's profile, including profiles provided by extensions. Updates never change it; to use another profile, recreate the schedule from a conversation using that profile. Kodelet resolves the profile again on the schedule's runner and directory at the start of every run.

Each run records the instruction, directory, and profile it used, when it was due, started, and finished, its outcome, its conversation, the agent's final message (up to 32 KiB), and stats: tool calls by tool, failed tool calls, and context compactions. Editing a schedule does not change the record of earlier runs.

Results show users Markdown, such as a table of runs or a run's final output, while the agent reads plain text. In [code mode](../../docs/codemode-design.md), each tool declares a one-line summary and an output schema, so scripts can read typed results from `reply.data`:

| Tool | `reply.data` |
| --- | --- |
| `schedule_list` | `{schedules, daemon}`; check `daemon.state` to see whether schedules will fire |
| `schedule_create`, `schedule_update` | `{schedule, upcomingRuns, daemon}` |
| `schedule_delete` | `{id, name}` |
| `schedule_history` | `{runs}`, each with its outcome, `durationSeconds`, and `stats` |
| `schedule_run_details` | `{run}`, adding `instruction`, `cwd`, `profile`, and `output` |

Creating, updating, and deleting a schedule needs the user's approval in an interactive client. Without one, for example in `kodelet run` or a scheduled run, these tools refuse, so unattended agents cannot plant or alter schedules.

To skip approval, set `auto_approve: true` in the schedule config file (see [settings](#run-the-daemon)) and `/reload`, or set `KODELET_SCHEDULE_AUTO_APPROVE=true` in the environment where the Kodelet server starts and restart the server. The tools then save changes without asking, including in `kodelet run` and in scheduled runs, so any agent with these tools can create schedules that keep running. Results note that a change was approved automatically. The extension reads this only from its own settings, not from Kodelet's `extensions.config`, because a repository's `kodelet-config.yaml` can set that.

### Schedule syntax

Schedules use five-field cron expressions (`minute hour day-of-month month day-of-week`) or descriptors such as `@hourly`, `@daily`, `@weekly`, and `@every 6h`. Prefix `CRON_TZ=<IANA zone>` to pick a timezone, for example `CRON_TZ=Europe/London 0 9 * * 1-5`; otherwise the host's local time applies. Schedules may fire at most once a minute.

## Run the daemon

```bash
kodelet-extension-schedule daemon
```

The daemon checks for due schedules, starts each run with `kodelet acp --runner <workspace>` in the schedule's directory and profile, and records its conversation and outcome. It reaches the server the same way the `kodelet` CLI does, so a local server starts automatically, and remote servers need `server` configuration and `kodelet auth login`.

| Flag | Setting | Default | Purpose |
| --- | --- | --- | --- |
| `--db` | `db` | see [storage](#storage) | Schedule database |
| `--server` | `server` | CLI configuration | Kodelet server URL |
| `--kodelet` | `kodelet` | `kodelet` | `kodelet` executable used for runs |
| `--poll-interval` | `poll_interval` | `30s` | How often to check for due schedules |
| `--concurrency` | `concurrency` | `2` | Maximum number of runs at once |
| `--run-timeout` | `run_timeout` | `1h` | Cancel runs that take longer; `0` disables |
| `--misfire-grace` | `misfire_grace` | `1h` | Skip runs missed by more than this |
| `--shutdown-grace` | `shutdown_grace` | `30s` | Time for runs to finish on shutdown before they are cancelled |

Each setting can also come from a `KODELET_SCHEDULE_<SETTING>` environment variable, such as `KODELET_SCHEDULE_POLL_INTERVAL=1m`, or from a YAML config file. Flags take precedence over environment variables, which take precedence over the config file. The config file is `schedule/config.yaml` under `$KODELET_BASE_PATH` (default `~/.kodelet`) when it exists, or the file named by `--config` or `KODELET_SCHEDULE_CONFIG`. Write durations as strings such as `90s` or `2h`:

```yaml
poll_interval: 1m
concurrency: 1
run_timeout: 30m
```

The extension reads the same `db` setting, so a database configured in the config file applies to both.

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
    <string>/Users/you/.local/bin/kodelet-extension-schedule</string>
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
ExecStart=%h/.local/bin/kodelet-extension-schedule daemon
Restart=on-failure

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now kodelet-schedule
```

Service managers start with a minimal environment. If the daemon starts the local Kodelet server, the server inherits that environment, so either start the server yourself first or add the provider credentials it needs to the service definition.

## Storage

Schedules, the latest 20 runs of each schedule, and the daemon heartbeat live in the `db` setting, or `schedule/schedule.db` under `$KODELET_BASE_PATH` (default `~/.kodelet`). The extension and daemon must resolve the same path; set it in the config file or `KODELET_SCHEDULE_DB` rather than with `--db`, which only the command you run sees.

The extension and daemon apply pending schema migrations when they open the database, recording them in its `schema_migrations` table. An older executable refuses a database migrated by a newer one, so upgrade the extension and daemon together. To change the schema, append a migration to `scheduleMigrations` in `migrations.go`; never edit a released one.

The extension runs on the runner's host, so schedules are stored on that host and the daemon must run there too. For a remote runner, run the daemon on the runner's host with CLI access to the server.

List the schedules of every workspace on this host:

```bash
kodelet-extension-schedule list
kodelet-extension-schedule list --json
```
