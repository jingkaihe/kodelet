<p align="center">
  <img src="pkg/webui/frontend/src/assets/logo.svg" width="40" height="40" alt="">
</p>

<h1 align="center">Kodelet</h1>

<p align="center">A minimal, extensible coding agent that outlives your terminal.</p>

[Kodelet](https://kodelet.com/) runs agentic loops on a server instead of your terminal session. Start a task in the terminal, pick it up in the browser or an editor that supports [ACP](https://agentclientprotocol.com/), and disconnect whenever you like. Keep everything on your laptop, or host the server remotely and send work to [runners](docs/MANUAL.md#workspace-bound-runners) on other machines.

Kodelet comes with a minimal core but is highly customizable through its powerful [extension system](docs/extension-design.md). Add custom tools and commands, intercept prompts and tool calls, build interactive terminal widgets, or plug in MCP servers, using TypeScript, Python, or any language that speaks JSON-RPC. Bring your own Claude, OpenAI, or OpenAI-compatible models, add skills for specialist know-how, and control access with sign-in through your identity provider.

## Get started

Kodelet supports macOS and Linux on amd64 and arm64.

### Install

With Homebrew:

```bash
brew tap jingkaihe/kodelet
brew install kodelet
```

Or use the install script:

```bash
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash

# Force standalone binary install
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash -s -- --binary
```

The install script defaults to package-based installation: Homebrew on macOS and `.deb`/`.rpm` packages on Linux.

### Connect a model

For a fresh installation, generate named model profiles and set your API key **before starting Kodelet**:

```bash
kodelet setup
export OPENAI_API_KEY="your-api-key"
```

Setup creates `openai` and `anthropic` profiles and selects `profile: openai`. Model settings live inside `profiles`; the top-level `profile` selects the default for new conversations. Choose another configured profile with `kodelet chat --profile anthropic`. Shared extension, tracing, and workspace settings remain top-level.

See the [provider guide](docs/MANUAL.md#llm-providers) and [sample configuration](config.sample.yaml) for other providers, authentication options, and model profiles. Model configuration and provider credentials belong to the daemon; if it is already running, finish active work and run `kodelet server restart` from the shell with the updated environment.

### Start in your terminal

From the directory you want to work in:

```bash
kodelet chat
```

Try a small, verifiable task:

> Explain this project's structure and how to run its tests. Do not edit files.

Then ask for a change, investigate a problem, or work through a plan together. Kodelet can edit files and execute commands, so start in a workspace you trust and review its changes and verification results.

### Pick up in your browser

```bash
kodelet server url --open
```

This starts the local daemon if needed and opens the Web UI. Terminal chat and the browser share that daemon and its saved conversations. Exiting terminal chat leaves work running; use `/stop` when you want to cancel it.

## Put it to work

Use `kodelet run` for one-off tasks or as part of a shell pipeline:

```bash
# Run a task directly
kodelet run "Fix the failing tests and verify the changes."

# Pipe in context from another command
git diff main | kodelet run "Review these changes for correctness and missing tests."
```

Add `--result-only` to output just the final response for use in scripts.

## Make it yours

Start with project instructions, then add capabilities as you need them:

| Capability | Use it for |
| --- | --- |
| [`AGENTS.md`](docs/MANUAL.md#agent-context-files) | Project context, conventions, and build or test instructions loaded automatically. |
| [Recipes](docs/FRAGMENTS.md) | Reusable prompts with arguments and shell substitutions for tasks you repeat. |
| [Skills](docs/SKILLS.md) | Domain guidance the agent loads when relevant to the task. |
| [Extensions](docs/extension-design.md) | Executable tools, commands, and lifecycle behavior that connect the agent to your workflows. |
| [MCP tools](sdk/src/extensions/mcp/README.md) | Tools from local or remote MCP servers, connected through the SDK MCP extension. |
| [Plugins](docs/SKILLS.md#managing-skills-with-plugins) | Bundles of skills, recipes, and extensions you can install and share. |

For example, with an image-generation extension installed, a request in terminal chat can produce an image you open in the Web UI.

With remote runners, install workspace skills, recipes, and extensions on the runner host, the machine that actually uses them.

## Documentation

- [User manual](docs/MANUAL.md): commands, configuration, providers, and troubleshooting.
- [Remote runners](docs/MANUAL.md#workspace-bound-runners): work on another machine's workspace.
- [Sample configuration](config.sample.yaml): model profiles, runner settings, and permissions.
- [TypeScript SDK](sdk/README.md): build applications and extensions around Kodelet.
- [Python SDK](https://github.com/jingkaihe/kodelet-python-sdk/blob/main/README.md): run agent sessions and write extensions in Python.
- [Skills collection](https://github.com/jingkaihe/skills/blob/main/README.md): reusable skills for specialist tools and workflows.
- [Subagent extension](https://github.com/jingkaihe/kodelet-subagent/blob/main/README.md): delegate tasks to background agents you can monitor, steer, and resume.

## Development

Kodelet uses `mise` for tool versions and development tasks:

```bash
mise install
mise run install
mise run lint
mise run test
```

Run `mise tasks` to list the remaining build, frontend, SDK, packaging, and container tasks.

## License

Kodelet is licensed under the [MIT License](LICENSE).
