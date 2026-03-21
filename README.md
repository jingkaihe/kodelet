# Kodelet

Kodelet is a lightweight agentic SWE Agent. It runs as an interactive CLI tool in your terminal. It is capable of peforming software engineering and production operating tasks.

## Key Features

- **Intelligent Engineering Assistant**: Automates software engineering tasks and production operations with agentic capabilities.
- **Interactive Architecture Design**: Collaboratively design and refine system architectures through natural dialogue.
- **Continuous Code Intelligence**: Analyzes, understands, and improves your codebase while answering technical questions in context.
- **Reusable Fragments/Receipts**: Create template-based prompts with variable substitution and bash command execution for routine tasks.
- **Vision Capabilities**: Support for image inputs including screenshots, diagrams, and mockups (Anthropic Claude models).

## Installation

### Via Homebrew (macOS/Linux)

```bash
brew tap jingkaihe/kodelet
brew install kodelet
```

### Via Install Script

```bash
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash

# Force standalone binary install
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash -s -- --binary
```

The install script defaults to package-based installation: Homebrew on macOS and `.deb`/`.rpm` packages on Linux.

## Choice of LLM

Kodelet supports Anthropic Claude, OpenAI compatible models and Google Gemini. The default model is now OpenAI `gpt-5.4`, with `gpt-5.4-mini` as the default weak model.

If you prefer explicit environment configuration, you can use:

```bash
export KODELET_PROVIDER="openai"
export KODELET_MODEL="gpt-5.4"
export OPENAI_API_KEY="your-api-key"
```

## Development

For detailed development instructions, including prerequisites, running locally, configuration options, and available mise tasks, please see the [Development Guide](docs/DEVELOPMENT.md).

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details..
