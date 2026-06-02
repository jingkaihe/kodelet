# Kodelet quick start

## Installation

```bash
# Package-based install by default where available
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash

# Force standalone binary install
curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash -s -- --binary
```

Show version and build info:

```bash
kodelet version
```

## Core usage modes

### One-shot mode

```bash
kodelet run "your query"
kodelet run -f "continue the task" # same as --follow
kodelet run --resume CONVERSATION_ID "more questions"
kodelet run --no-save "temporary query"
kodelet run --result-only "what is 2+2"
kodelet run --no-tools "what is the capital of France?"
```

### Interactive/IDE mode (ACP)

Kodelet implements the Agent Client Protocol (ACP):

```bash
kodelet acp
```

Example Zed-style configuration:

```json
{
  "agent": {
    "command": "kodelet",
    "args": ["acp"]
  }
}
```

ACP supports session persistence, image input, embedded file context, streaming responses, and tool-call visualization in compatible clients.

### Web UI

```bash
kodelet serve
kodelet serve --host 0.0.0.0 --port 3000
kodelet serve --cors-origins https://app.example.com,https://admin.example.com
```

Open the tokenized URL printed by `kodelet serve`. Loopback CORS origins are allowed by default; use `--cors-origins` for additional browser origins.

## Project context

Kodelet automatically loads `AGENTS.md` from the current repository. Good context files include project structure, tech stack, build/test/lint commands, coding style, and deployment notes.

Bootstrap one:

```bash
kodelet run -r init
```

## Git helpers

```bash
git add .

# Fast, non-interactive commit message generation
kodelet commit --no-confirm

# Include a ticket prefix
kodelet commit --prefix TICKET-123

# Interactive commit message flow
kodelet commit

# Pull requests
kodelet pr
kodelet pr --target main
kodelet pr --draft
```

## Image input

```bash
kodelet run --image /path/to/screenshot.png "What's wrong with this UI?"
kodelet run --image ./diagram.png --image ./mockup.jpg "Compare these designs"
```

Supported formats: JPEG, PNG, GIF, WebP. Limits: 5 MB per image, 10 images per message. Provider/model must support multimodal input.

## Shell completion

```bash
# Bash
echo 'source <(kodelet completion bash)' >> ~/.bashrc

# Zsh
echo 'source <(kodelet completion zsh)' >> ~/.zshrc

# Fish
kodelet completion fish > ~/.config/fish/completions/kodelet.fish
```

## Common workflows

```bash
# Review changes
git diff main | kodelet run "review these changes for issues"

# Investigate and then implement
kodelet run "analyze error logs and suggest fixes"
kodelet run -f "implement the suggested fix" # same as --follow

# Refactor or test
kodelet run "refactor user authentication to use middleware pattern"
kodelet run "write unit tests for the payment processing module"
```
