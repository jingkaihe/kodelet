# Fragments/Receipts System

Kodelet's fragments (also called "receipts") system allows you to create reusable prompt templates with dynamic content generation. Fragments support variable substitution and bash command execution, making complex queries maintainable and shareable.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Template Syntax](#template-syntax)
- [Directory Structure](#directory-structure)
- [Command Line Usage](#command-line-usage)
- [Example Fragments](#example-fragments)
- [Advanced Usage](#advanced-usage)
- [Best Practices](#best-practices)

## Overview

Fragments solve the problem of repeatedly typing lengthy, complex instructions by allowing you to:

- **Create reusable templates** for common tasks
- **Execute bash commands** and embed their output
- **Pass dynamic arguments** for customization
- **Share fragments** across projects and team members
- **Maintain consistency** in prompt formatting

## Quick Start

### 1. Create a Fragment

Create a directory for fragments and add your first template:

```bash
mkdir -p ./receipts
```

Create `./receipts/commit.md`:
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

## Directory Structure

Fragments are discovered in two locations with precedence order:

1. **`./receipts/`** - Repository-specific fragments (higher precedence)
2. **`~/.kodelet/receipts/`** - User-global fragments

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
- `./receipts/commit.md`
- `~/.kodelet/receipts/commit.md`

The local repository version (`./receipts/commit.md`) will be used.

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

- `-r, --receipt FRAGMENT` - Specify the fragment/receipt to use
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

### Git Commit Assistant (`./receipts/commit.md`)

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

### Personal Introduction (`./receipts/intro.md`)

```markdown
What is your name?

My name is {{.name}}.
My occupation is {{.occupation}}.

Write a short introduction about me.
```

Usage: `kodelet run -r intro --arg name="Alice Smith" --arg occupation="Software Engineer"`

### Code Review Template (`./receipts/code-review.md`)

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

### Project Analysis (`./receipts/analyze.md`)

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

## Best Practices

### 1. Use Descriptive Names

```bash
# Good
./receipts/git-commit-analyzer.md
./receipts/code-review-golang.md
./receipts/deploy-checklist.md

# Avoid
./receipts/script.md
./receipts/temp.md
./receipts/x.md
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
./receipts/           # Committed with the project

# Global fragments (optional)
~/.kodelet/receipts/  # Personal collection
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