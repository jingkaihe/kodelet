<p align="center">
  <img src=".github/logo.svg" width="40" height="40" alt="">
</p>

<h1 align="center">Kodelet</h1>

**Your tools. Your models. Your agent.**

[Kodelet](https://kodelet.com/) is an open-source coding agent built for software engineering and the work around it. Use it to understand a codebase, implement and verify changes, investigate failures, and automate repeatable work. Extend it with specialist tools and expertise to take it beyond coding.

Start a task in your terminal, follow it in your browser, or work through an ACP-compatible editor. You choose the model, the machine doing the work, and the capabilities available to the agent.

## Why Kodelet?

- **Close the terminal. Keep the work going.** A background daemon owns execution and saves your conversations. Leave terminal chat without stopping the task, follow the same conversation in the Web UI, and return without starting over.
- **Your terminal. Another machine.** Use the built-in runner for local work, or connect a runner on another machine to work with its files, dependencies, and environment. Your client does not need to be where the work happens.
- **Bring the tools you rely on.** Skills, recipes, and executable extensions add expertise, repeatable workflows, and new tools. Plugins package them for reuse, including capabilities beyond coding, such as image generation and integrations with external services.
- **Pick the model for the job.** Use Anthropic Claude, OpenAI, or an OpenAI-compatible endpoint with your own credentials. Choose a configured model profile and reasoning effort before starting a conversation.

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

For a fresh installation using the default OpenAI provider, set your API key **before starting Kodelet**:

```bash
export OPENAI_API_KEY="your-api-key"
```

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
