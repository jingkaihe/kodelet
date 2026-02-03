# Fragments/Recipes System

Kodelet's fragments (also called "recipes") system allows you to create reusable prompt templates with dynamic content generation. Fragments support variable substitution and bash command execution, making complex queries maintainable and shareable.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Template Syntax](#template-syntax)
  - [Variable Substitution](#variable-substitution)
  - [Bash Command Execution](#bash-command-execution)
  - [Combining Variables and Commands](#combining-variables-and-commands)
  - [Default Values](#default-values)
- [Subagent Workflows](#subagent-workflows)
- [Directory Structure](#directory-structure)
- [Command Line Usage](#command-line-usage)
- [Example Fragments](#example-fragments)
- [Advanced Usage](#advanced-usage)
- [Recipe Hooks](#recipe-hooks)
- [Best Practices](#best-practices)

## Overview

Fragments solve the problem of repeatedly typing lengthy, complex instructions by allowing you to:

- **Create reusable templates** for common tasks
- **Execute bash commands** and embed their output
- **Pass dynamic arguments** for customization
- **Share fragments** across projects and team members
- **Maintain consistency** in prompt formatting

### Built-in Recipes

Kodelet includes several built-in recipes for common tasks:

- **`init`** - Bootstrap `AGENTS.md` file with workspace context and conventions
- **`commit`** - Generate git commit messages from staged changes
- **`compact`** - Compact conversation context into a comprehensive summary (uses recipe hooks)
- **`custom-tool`** - Create custom tools for Kodelet
- **`github/pr`** - Generate pull request descriptions
- **`github/issue-resolve`** - Resolve GitHub issues
- **`github/pr-respond`** - Respond to pull request comments

List all available recipes with:
```bash
kodelet recipe list
```

View a recipe's content and metadata:
```bash
kodelet recipe show init
```

## Quick Start

### 1. Create a Fragment

Create a directory for fragments and add your first template:

```bash
mkdir -p ./.kodelet/recipes
```

Create `./.kodelet/recipes/commit.md`:
```markdown
## Context:

The current git status:

{{bash "git" "status"}}

The git diff against the main branch:

{{bash "git" "diff" "main"}}

## Task:
Please review the above git status and diff, and create a git commit message that follows conventional commit standards.
```

### 2. Use the Fragment

```bash
kodelet run -r commit
```

This will execute the git commands, substitute their output into the template, and send the complete prompt to the LLM.

### Repository Initialization

The built-in `init` recipe is designed to bootstrap your repository's `AGENTS.md` file:

```bash
# Initialize or improve AGENTS.md for your repository
kodelet run -r init
```

This recipe will:
- Analyze your repository structure, tech stack, and architecture
- Identify build systems, testing frameworks, and key commands
- Extract coding conventions and patterns from existing code
- Review any existing AI assistant rules (Cursor, Copilot)
- Create or suggest improvements to `AGENTS.md`

The `AGENTS.md` file provides context that helps Kodelet work more effectively in your workspace by understanding your project's conventions, commands, and architecture.

## Template Syntax

Fragments use Go's `text/template` syntax with custom functions for enhanced functionality.

### Variable Substitution

Use `{{.variable_name}}` to insert dynamic values:

```markdown
Hello {{.name}}!

Your role: {{.occupation}}
Project: {{.project}}
```

Usage:
```bash
kodelet run -r greeting --arg name="Alice" --arg occupation="Engineer" --arg project="Kodelet"
```

### Bash Command Execution

Use `{{bash "command" "arg1" "arg2" ...}}` to execute commands and embed their output:

```markdown
Current date: {{bash "date" "+%Y-%m-%d"}}
Git status: {{bash "git" "status" "--short"}}
File count: {{bash "find" "." "-name" "*.go" "-type" "f" | "wc" "-l"}}
```

For complex shell commands, use `sh -c`:
```markdown
Top 5 largest files: {{bash "sh" "-c" "find . -type f -exec ls -la {} + | sort -k5 -nr | head -5"}}
```

### Combining Variables and Commands

```markdown
## Review for {{.project_name}}

Current branch: {{bash "git" "branch" "--show-current"}}
Last commit by {{.author}}: {{bash "git" "log" "-1" "--pretty=format:%s"}}

Please analyze the {{.project_name}} codebase focusing on {{.focus_area}}.
```

### Default Values

Kodelet supports two complementary approaches for providing default values to fragment arguments:

#### 1. YAML Metadata Arguments (Recommended for Common Arguments)

Define arguments with descriptions and default values in the fragment's YAML frontmatter:

```markdown
---
name: Docker Build Recipe
description: Build and tag a Docker image
arguments:
  image:
    description: The Docker image name (required)
  tag:
    description: Image tag to use
    default: latest
  platform:
    description: Target platform for the build
    default: linux/amd64
  context:
    description: Build context directory
    default: .
  dockerfile:
    description: Path to the Dockerfile
    default: Dockerfile
---

Building Docker image:
- Image: {{.image}} (required - no default)
- Tag: {{.tag}}
- Platform: {{.platform}}
- Context: {{.context}}
- Dockerfile: {{.dockerfile}}
```

Usage:
```bash
# Use all defaults, only provide required image arg
kodelet run -r docker-build --arg image=myapp

# Override specific defaults
kodelet run -r docker-build --arg image=myapp --arg tag=v1.2.3 --arg platform=linux/arm64
```

**When to use YAML arguments:**
- For expected arguments that users commonly customize
- To make your fragment's interface self-documenting with descriptions
- When you want defaults to be discoverable via `kodelet recipe show`

#### 2. Template `default` Function (For Optional Values)

Use the `default` function for truly optional values inline in your template:

```markdown
Branch: {{default .branch "main"}}
Environment: {{default .env "development"}}
Optional message: {{default .message "No message provided"}}
```

Usage:
```bash
# Uses all inline defaults
kodelet run -r deployment

# Override specific values
kodelet run -r deployment --arg branch=feature-x --arg message="Hotfix deployment"
```

**When to use template defaults:**
- For truly optional values that may not apply to all use cases
- For conditional defaults based on other values
- When you need different defaults in different parts of the template

#### 3. Hybrid Approach (Best of Both)

Combine both approaches for maximum flexibility:

```markdown
---
name: Deployment Recipe
description: Deploy application with sensible defaults
arguments:
  branch:
    description: Branch to deploy
    default: main
  env:
    description: Target environment
    default: development
---

Deploying {{.branch}} to {{.env}}
Optional message: {{default .message "Standard deployment"}}
Build args: {{default .build_args "none"}}

{{if ne (default .notify "false") "false"}}
Notifications enabled: {{.notify}}
{{end}}
```

This gives you:
- **YAML arguments** for expected arguments with descriptions (branch, env)
- **Template defaults** for truly optional fields (message, build_args, notify)
- **Clean, self-documenting** fragment interface

### Subagent Workflows

Recipes can be marked as **workflows**, which allows them to be invoked by the subagent tool. This enables the model to delegate specialized tasks like PR creation, issue resolution, or autonomous development loops.

#### Marking a Recipe as a Workflow

Add `workflow: true` to the recipe's YAML frontmatter:

```markdown
---
name: My Custom Workflow
description: A workflow that can be delegated to a subagent
workflow: true
arguments:
  target:
    description: Target branch to operate on
    default: main
  mode:
    description: Operation mode (fast or thorough)
    default: thorough
---

Instructions for the workflow...
```

#### How Workflows Work

When workflows are enabled, the subagent tool's description includes available workflows. The model can then invoke workflows like:

```json
{"workflow": "github/pr", "args": {"target": "develop", "draft": "true"}}
```

The `question` parameter becomes optional when a workflow is specified - the workflow's predefined instructions are used instead.

#### Disabling Workflows

You can disable workflow support for security or debugging:

```bash
# Disable workflows for run command
kodelet run --no-workflows "query"

# Disable workflows for ACP mode
kodelet acp --no-workflows
```

#### When to Create a Workflow

Create a workflow when:
- The task is self-contained and doesn't require back-and-forth with the user
- The task has well-defined inputs and outputs
- You want the model to be able to delegate this task autonomously
- Examples: PR creation, code generation, documentation generation

Keep as a regular recipe when:
- The task requires user interaction or confirmation
- The task is exploratory and benefits from user guidance
- You only want explicit user invocation via `-r` flag

## Directory Structure

Fragments are discovered from multiple locations with precedence order:

1. **`./.kodelet/recipes/`** - Repository-local standalone recipes (highest precedence)
2. **`./.kodelet/plugins/<org@repo>/recipes/`** - Repository-local plugin recipes
3. **`~/.kodelet/recipes/`** - User-global standalone recipes
4. **`~/.kodelet/plugins/<org@repo>/recipes/`** - User-global plugin recipes
5. **Built-in recipes** - Embedded in the binary (lowest precedence)

### File Naming

Fragments can have two naming patterns:
- `fragment-name.md` - Markdown files (recommended)
- `fragment-name` - Extensionless files

When referencing fragments, omit the `.md` extension:
```bash
kodelet run -r my-fragment  # Finds my-fragment.md or my-fragment
```

### Precedence Example

If you have:
- `./.kodelet/recipes/commit.md`
- `~/.kodelet/recipes/commit.md`

The local repository version (`./.kodelet/recipes/commit.md`) will be used.

### Plugin-based Recipes

Recipes installed via plugins are prefixed with `org/repo/` to avoid naming conflicts:
- `jingkaihe/recipes/deploy` - Deploy recipe from the `jingkaihe/recipes` plugin
- `anthropic/tools/analyze` - Analyze recipe from the `anthropic/tools` plugin

Standalone recipes use simple names without prefix:
- `my-recipe` - Standalone recipe at `.kodelet/recipes/my-recipe.md`

### Managing Recipes with Plugins

Use the unified plugin system to install recipes from GitHub repositories:

```bash
# Install all skills/recipes from a GitHub repository
kodelet plugin add orgname/repo

# Install to global directory
kodelet plugin add orgname/repo -g

# List all installed plugins
kodelet plugin list

# Remove a plugin
kodelet plugin remove org/repo
```

See [docs/SKILLS.md](./SKILLS.md) for more details on the plugin system.

## Command Line Usage

### Basic Fragment Usage

```bash
# Use fragment without arguments
kodelet run -r fragment-name

# Use fragment with arguments
kodelet run -r fragment-name --arg key1=value1 --arg key2=value2

# Combine fragment with additional instructions
kodelet run -r fragment-name "Make it more detailed"
```

### Flag Reference

- `-r, --recipe FRAGMENT` - Specify the fragment/recipe to use
- `--arg KEY=VALUE` - Pass arguments to the fragment (repeatable)

### Examples

```bash
# Simple usage
kodelet run -r commit

# With arguments
kodelet run -r intro --arg name="John Doe" --arg occupation="Software Engineer"

# Multiple arguments
kodelet run -r project-review \
  --arg project_name="Kodelet" \
  --arg language="Go" \
  --arg focus_area="error handling"

# Fragment with additional context
kodelet run -r commit "Focus on the breaking changes"
```

## Example Fragments

### Git Commit Assistant (`./.kodelet/recipes/commit.md`)

```markdown
## Context:

The current git status:

{{bash "git" "status"}}

The git diff against the main branch:

{{bash "git" "diff" "main"}}

## Task:
Please review the above git status and diff, and create a git commit message that follows conventional commit standards.
```

Usage: `kodelet run -r commit`

### Personal Introduction (`./.kodelet/recipes/intro.md`)

```markdown
What is your name?

My name is {{.name}}.
My occupation is {{.occupation}}.

Write a short introduction about me.
```

Usage: `kodelet run -r intro --arg name="Alice Smith" --arg occupation="Software Engineer"`

### Code Review Template (`./.kodelet/recipes/code-review.md`)

```markdown
## Code Review Request

### Repository Context:
Branch: {{bash "git" "branch" "--show-current"}}
Last 3 commits: {{bash "git" "log" "--oneline" "-3"}}

### Files Changed:
{{bash "git" "diff" "--name-only" "HEAD~1"}}

### Diff Summary:
{{bash "git" "diff" "--stat" "HEAD~1"}}

### Review Focus:
Please review the above changes focusing on:
- Code quality and best practices
- Potential bugs or issues
- Performance implications
- Documentation completeness

{{if .specific_concerns}}
### Specific Concerns:
{{.specific_concerns}}
{{end}}
```

Usage: `kodelet run -r code-review --arg specific_concerns="Check the error handling in the new functions"`

### Project Analysis (`./.kodelet/recipes/analyze.md`)

```markdown
## {{.project_name}} Project Analysis

### Project Structure:
{{bash "find" "." "-type" "f" "-name" "*.{{.extension}}" | "head" "-20"}}

### Dependency Information:
{{bash "cat" "go.mod"}}

### Recent Activity:
{{bash "git" "log" "--oneline" "--since={{.time_period}}"}}

### Task:
Please analyze this {{.project_name}} project written in {{.language}}. 
Focus on: {{.focus_areas}}
```

Usage:
```bash
kodelet run -r analyze \
  --arg project_name="Kodelet" \
  --arg language="Go" \
  --arg extension="go" \
  --arg time_period="1 week ago" \
  --arg focus_areas="architecture, testing, documentation"
```

## Advanced Usage

### Conditional Logic

Use Go template conditionals for flexible fragments:

```markdown
## {{.task_type}} Task

{{if eq .task_type "bug-fix"}}
### Bug Context:
Issue: {{.issue_number}}
Symptoms: {{.symptoms}}
{{else if eq .task_type "feature"}}
### Feature Requirements:
Feature: {{.feature_name}}
Requirements: {{.requirements}}
{{end}}

### Current State:
{{bash "git" "status"}}
```

### Nested Templates

```markdown
## System Information

### Environment:
- OS: {{bash "uname" "-a"}}
- Go Version: {{bash "go" "version"}}
- Git Version: {{bash "git" "--version"}}

### Project Details:
{{template "project-info" .}}

{{define "project-info"}}
- Name: {{.project_name}}
- Language: {{.language}}
- Last Updated: {{bash "git" "log" "-1" "--format=%cd"}}
{{end}}
```

### Error Handling

Fragments gracefully handle command failures:

```markdown
Git status: {{bash "git" "status"}}
Node version: {{bash "node" "--version"}}  <!-- Will show error if Node.js not installed -->
```

If a command fails, you'll see: `[ERROR executing command 'node --version': executable not found]`

## Recipe Hooks

Recipes can declare lifecycle hooks that execute built-in handlers at specific points during conversation processing. This enables advanced features like context compaction.

### Hook Configuration

Hooks are declared in the recipe's YAML frontmatter:

```yaml
---
name: my-recipe
description: Recipe with hooks
hooks:
  turn_end:
    handler: swap_context
    once: true
allowed_tools: []
---
```

### Hook Configuration Options

| Option | Type | Description |
|--------|------|-------------|
| `handler` | string | Name of the built-in handler to invoke |
| `once` | boolean | If true, handler only executes on the first turn (default: false) |

### Available Hook Events

| Event | Description |
|-------|-------------|
| `turn_end` | Fires after each assistant response, before the next user message |

### Built-in Handlers

| Handler | Event | Description |
|---------|-------|-------------|
| `swap_context` | `turn_end` | Replaces conversation history with the assistant's response |

### Example: Context Compaction

The built-in `compact` recipe demonstrates recipe hooks:

```markdown
---
name: compact
description: Compact the conversation context into a comprehensive summary
hooks:
  turn_end:
    handler: swap_context
    once: true
allowed_tools: []
---
Create a comprehensive summary of the conversation history...
```

When used:
1. The recipe prompt asks the LLM to generate a summary
2. After the LLM responds, the `turn_end` hook fires
3. The `swap_context` handler replaces the conversation history with the summary
4. The conversation continues with the compacted context

Usage:
```bash
# Compact the current conversation
kodelet run -r compact --follow

# Continue with the compacted context
kodelet run --follow "Now implement the next feature"
```

### Creating Custom Compaction Recipes

You can create custom compact recipes with different summarization strategies:

```markdown
---
name: compact-brief
description: Brief context compaction for quick summaries
hooks:
  turn_end:
    handler: swap_context
    once: true
allowed_tools: []
---
Create a brief summary of this conversation in 3-5 bullet points:
- What was the user's main goal?
- What key actions were taken?
- What is the current state?

Keep it concise and actionable.
```

For more details on hook types and payloads, see [Agent Lifecycle Hooks](./HOOKS.md).

## Best Practices

### 1. Use Descriptive Names

```bash
# Good
./.kodelet/recipes/git-commit-analyzer.md
./.kodelet/recipes/code-review-golang.md
./.kodelet/recipes/deploy-checklist.md

# Avoid
./.kodelet/recipes/script.md
./.kodelet/recipes/temp.md
./.kodelet/recipes/x.md
```

### 2. Document Your Fragments

Add comments in your fragments:

```markdown
<!-- 
This fragment analyzes git commits for a project review.
Usage: kodelet run -r git-analysis --arg days=7 --arg author="username"
-->

## Git Analysis for Last {{.days}} Days
...
```

### 3. Use Version Control

Keep your fragments in version control:

```bash
# Repository-specific fragments
./.kodelet/recipes/   # Committed with the project

# Global fragments (optional)
~/.kodelet/recipes/   # Personal collection
```

### 4. Validate Arguments

Design fragments to be robust:

```markdown
{{if not .project_name}}
ERROR: project_name argument is required
Usage: kodelet run -r this-fragment --arg project_name="YourProject"
{{else}}
## Analysis for {{.project_name}}
...
{{end}}
```

### 5. Test Your Fragments

Create test scenarios:

```bash
# Test with minimal arguments
kodelet run -r my-fragment --arg required_arg="test"

# Test with all arguments
kodelet run -r my-fragment --arg arg1="test1" --arg arg2="test2"

# Test error conditions
kodelet run -r my-fragment  # Missing required args
```

### 6. Use Consistent Formatting

Establish a team style:

```markdown
## Context:
[Command outputs and current state]

## Requirements:
[What needs to be done]

## Task:
[Clear instruction for the LLM]
```

### 7. Security Considerations

Be cautious with sensitive data:

```markdown
<!-- Avoid exposing secrets -->
Database URL: {{.db_url}}  <!-- Don't do this with secrets -->

<!-- Use environment variables in commands instead -->
Connection status: {{bash "check-db-connection"}}  <!-- Script checks securely -->
```

### 8. Command Best Practices

```markdown
<!-- Good: Explicit arguments -->
{{bash "git" "log" "--oneline" "-10"}}

<!-- Good: Shell commands when needed -->
{{bash "sh" "-c" "find . -name '*.log' | grep error"}}

<!-- Avoid: Commands that require interaction -->
<!-- {{bash "vim" "file.txt"}} Don't do this -->

<!-- Avoid: Long-running commands -->
<!-- {{bash "npm" "install"}} Be careful with slow commands -->
```

This fragment system makes Kodelet incredibly powerful for automating complex, context-aware queries while maintaining reusability and consistency across your development workflow.
