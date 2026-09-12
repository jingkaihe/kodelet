# Kodelet

**Your tools. Your models. Your agent.**

Kodelet is an open-source AI agent that works with your files, tools, and machines, not just your code. Use it to build software, investigate failures, turn notes into plans, and automate repeatable work. Extend it with the specialist tools and expertise your work needs.

Start a task in your terminal, follow it in your browser, or work through an ACP-compatible editor. You choose the model, the machine doing the work, and the capabilities available to the agent.

[Website and demos](https://kodelet.com/) · [User manual](docs/MANUAL.md) · [TypeScript SDK](sdk/README.md)

## Why Kodelet?

- **Close the terminal. Keep the work going.** A background daemon owns execution and saves your conversations. Leave terminal chat without stopping the task, follow the same conversation in the Web UI, and return without starting over.
- **Your terminal. Another machine.** Use the built-in runner for local work, or connect a runner on another machine to work with its files, dependencies, and environment. Your client does not need to be where the work happens.
- **Bring the tools you rely on.** Skills, recipes, and executable extensions add expertise, repeatable workflows, and new tools. Plugins package them for reuse, including capabilities beyond coding, such as image generation and integrations with external services.
- **Pick the model for the job.** Use Anthropic Claude, OpenAI, or an OpenAI-compatible endpoint with your own credentials. Choose a configured model profile and reasoning effort before starting a conversation.
- **Give longer work a clear objective.** Set a persistent `/goal`, steer active work as requirements change, and resume saved conversations. Goals stay with the conversation across resume and context compaction.

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
kodelet server start
kodelet server url --open
```

Terminal chat starts the local daemon automatically; `server start` also lets you start it without opening chat. Both interfaces share that daemon and its saved conversations. Exiting terminal chat leaves work running; use `/stop` when you want to cancel it.

## Put it to work

Use interactive chat for ongoing work, or `kodelet run` for focused tasks and shell workflows:

```bash
# Implement and verify a change
kodelet run "Fix the failing parser test, add a regression test, and run the parser test suite."

# Investigate an operational problem
kodelet run "Read the logs in ./logs, explain the likely cause of the failures, and suggest next checks. Do not change files."

# Work with documents, not just code
kodelet run "Turn the notes in ./notes into a project brief with decisions, open questions, and next steps."

# Bring existing shell output into the conversation
git diff main | kodelet run "Review these changes for correctness and missing tests."

# Give a multi-step task a persistent objective
kodelet run "/goal Finish the migration, update the documentation, and verify the tests pass."
```

Use `kodelet acp` to connect an ACP-compatible editor, or the [TypeScript SDK](sdk/README.md) to create, resume, and stream agent sessions in your own application. Git helpers (`kodelet commit` and `kodelet pr`) handle commit and pull request workflows, and [image inputs](docs/MANUAL.md#image-input-support) let supported models work with screenshots, diagrams, and mockups.

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
