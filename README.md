<p align="center">
  <img src="pkg/webui/frontend/src/assets/logo.svg" width="40" height="40" alt="">
</p>

<h1 align="center">Kodelet</h1>

[Kodelet](https://kodelet.com/) is an open-source coding agent that isn't tied to your laptop or terminal session. Run everything locally, or use a single control plane to manage multiple agent runs across remote machines. The agentic loop is decoupled from the execution environments, so the server coordinating the work doesn't have to be the machine doing it.

The terminal UI, Web UI, and ACP-compatible editors connect to the agent without owning its lifetime. Switch clients or disconnect while the work continues. Shape the agent with custom extension tools, MCP integrations, and agent skills, and access your server through identity-based sign-in.

## Why Kodelet?

- **Run locally or across remote machines.** Kodelet separates the backend that runs the agentic loops from the runners that access files and execute tools, allowing one backend to coordinate multiple agent runs across different machines. You can keep everything local or host both the backend and runners remotely so work continues when your laptop is offline.
- **Move between clients without interrupting the agent.** The TUI, Web UI, and ACP-compatible editors connect to the same backend so you can start a task in the terminal and follow the same conversation in your browser without restarting it. The backend manages the agent's lifetime independently of these clients, which means closing your terminal or browser doesn't stop the work.
- **Customize how the agent works.** Extensions let you change the agent's behavior by adding custom tools and commands or responding to lifecycle events. The SDK MCP extension brings MCP tools into this system alongside your own extensions, while skills and recipes provide specialist instructions and reusable prompts that you can package with extensions as plugins to install and share.
- **Sign in through your identity provider.** Kodelet supports OpenID Connect (OIDC) so you can use your existing identity to sign in on the web and approve access for CLI, TUI, and ACP clients through your browser. You can restrict access to specific email addresses or domains and assign roles to control who can use the server's terminal or administer runners.
- **Choose the model for each task.** Kodelet works with Anthropic Claude, OpenAI, and OpenAI-compatible endpoints using your own credentials. Model profiles let you select a model and its settings for each conversation so you can adjust how the agent reasons to suit the work you're doing.

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
