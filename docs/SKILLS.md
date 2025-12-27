# Agentic Skills

## Overview

Agentic Skills are model-invoked capabilities that package domain expertise into discoverable units. Unlike fragments/recipes which require explicit user invocation, skills are automatically invoked by Kodelet when it determines they are relevant to your task.

**Key Characteristics:**
- **Model-invoked**: Kodelet autonomously decides when to use skills based on task context
- **Domain expertise**: Skills package specialized knowledge (PDF handling, spreadsheet manipulation, etc.)
- **Context injection**: When invoked, skill instructions are loaded into the conversation context
- **Read-only**: Skill directories contain supporting files that should not be modified

## How Skills Work

1. **Discovery**: At startup, Kodelet discovers skills from configured directories
2. **Description**: Each skill has a name and description that help Kodelet decide when to use it
3. **Invocation**: When a task matches a skill's domain, Kodelet automatically invokes it
4. **Context Loading**: The skill's instructions and reference materials become available to Kodelet

## Creating a Skill

### Directory Structure

Each skill is a directory containing a `SKILL.md` file and optional supporting files:

```
~/.kodelet/skills/my-skill/
├── SKILL.md          (required - skill definition and instructions)
├── reference.md      (optional - additional documentation)
├── examples.md       (optional - usage examples)
├── scripts/
│   └── helper.py     (optional - utility scripts)
└── templates/
    └── template.txt  (optional - template files)
```

### SKILL.md Format

The `SKILL.md` file must contain YAML frontmatter with required fields:

```markdown
---
name: my-skill
description: Brief description of what this skill does and when to use it
---

# My Skill

## Instructions

Provide clear, step-by-step guidance for the agent when working with this domain.

### Step 1: Understanding the Task
Explain how to analyze the user's request...

### Step 2: Implementation
Describe the approach to take...

## Examples

### Example 1: Basic Usage
Show a concrete example...

### Example 2: Advanced Usage
Show a more complex example...

## Reference

Link to or include relevant documentation, APIs, or standards.

## Common Pitfalls

List common mistakes and how to avoid them.
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique identifier for the skill (used when invoking) |
| `description` | Yes | Brief description used by the model for decision-making |

## Skill Locations

Skills are discovered from two locations with the following precedence:

1. **Repository-local** (higher precedence): `./.kodelet/skills/<skill_name>/SKILL.md`
2. **User-global**: `~/.kodelet/skills/<skill_name>/SKILL.md`

Repository-local skills take precedence over user-global skills with the same name, allowing project-specific customizations.

## Managing Skills

Kodelet provides CLI commands to add, list, and remove skills from GitHub repositories.

### Adding Skills

```bash
# Add all skills from a GitHub repository (installs to ./.kodelet/skills/)
kodelet skill add orgname/skills

# Add a specific skill from a repository
kodelet skill add orgname/skills --dir skills/specific-skill
kodelet skill add orgname/skills -d skills/specific-skill

# Add skills from a specific version, branch, or commit SHA
kodelet skill add orgname/skills@v0.1.0
kodelet skill add orgname/skills@main
kodelet skill add orgname/skills@abc1234

# Add skills to global directory (~/.kodelet/skills/)
kodelet skill add orgname/skills -g
kodelet skill add orgname/skills --global
```

**Requirements:**
- GitHub CLI (`gh`) must be installed
- Must be authenticated (`gh auth login`)

### Listing Skills

```bash
# List all installed skills with their locations and descriptions
kodelet skill list
```

Example output:
```
NAME              DIRECTORY                              DESCRIPTION
----              ---------                              -----------
pdf               ./.kodelet/skills/pdf                  Handle PDF file operations...
xlsx              ~/.kodelet/skills/xlsx                 Work with Excel spreadsheets...
kubernetes        ~/.kodelet/skills/kubernetes           Manage Kubernetes clusters...
```

### Removing Skills

```bash
# Remove a skill from local directory (./.kodelet/skills/)
kodelet skill remove skill-name

# Remove a skill from global directory (~/.kodelet/skills/)
kodelet skill remove skill-name -g
kodelet skill remove skill-name --global
```

## Configuration

### Global Enable/Disable

In your `~/.kodelet/config.yaml` or `./kodelet-config.yaml`:

```yaml
# Skills configuration
skills:
  # Enable/disable skills globally (default: true when not specified)
  enabled: true
  
  # Allowlist of skill names (empty = all discovered skills enabled)
  # When specified, only these skills will be available
  allowed:
    - pdf
    - xlsx
    - kubernetes
```

### CLI Flags

Disable skills for a single session:

```bash
kodelet run --no-skills "your query"
kodelet chat --no-skills
```

## Working with Skills

### Supporting Files

Skills can include supporting files that Kodelet will read when needed:

- **Reference documentation**: Additional context and specifications
- **Example files**: Sample inputs/outputs for the domain
- **Scripts**: Utility scripts that can be copied and executed
- **Templates**: Starting points for generating output

### Script Usage

When a skill includes scripts, Kodelet follows these guidelines:

1. **Read-only skill directory**: Never modify files in the skill directory
2. **Copy before modify**: If a script needs modification, copy it to the working directory first
3. **Use uv for Python**: For Python scripts, use `uv` with inline metadata dependencies instead of system pip

Example workflow:
```bash
# Kodelet will:
# 1. Read the script from the skill directory
# 2. Copy it to the working directory if modifications are needed
# 3. Execute using uv for dependency management
```

## Example Skills

### PDF Skill

```markdown
---
name: pdf
description: Handle PDF file operations including extraction, manipulation, and generation
---

# PDF Processing Skill

## Instructions

When working with PDF files, follow these guidelines...

## Tools and Libraries

- Use `pypdf` for basic PDF operations
- Use `pdf2image` for PDF to image conversion
- Use `reportlab` for PDF generation

## Examples

### Extract text from PDF
...

### Merge multiple PDFs
...
```

### Excel/Spreadsheet Skill

```markdown
---
name: xlsx
description: Work with Excel spreadsheets and CSV files for data analysis and manipulation
---

# Excel/Spreadsheet Processing Skill

## Instructions

When working with spreadsheet data...

## Recommended Libraries

- Use `openpyxl` for Excel files
- Use `pandas` for data manipulation
- Use `xlsxwriter` for creating formatted Excel files

## Examples
...
```

### Kubernetes Skill

```markdown
---
name: kubernetes
description: Manage and troubleshoot Kubernetes clusters, deployments, and configurations
---

# Kubernetes Operations Skill

## Instructions

When working with Kubernetes...

## Common Commands
...

## Troubleshooting Guide
...
```

## Best Practices

### Writing Effective Skills

1. **Clear descriptions**: Write descriptions that help the model understand when to invoke the skill
2. **Structured instructions**: Use clear headings and step-by-step guidance
3. **Include examples**: Provide concrete examples for common use cases
4. **Document pitfalls**: List common mistakes and how to avoid them
5. **Keep focused**: Each skill should cover a specific domain, not be overly broad

### Organizing Skills

1. **Repository-local for project-specific**: Use `.kodelet/skills/` for skills specific to a project
2. **Global for personal workflows**: Use `~/.kodelet/skills/` for skills you use across projects
3. **Share via version control**: Commit repository-local skills to share with your team

### Security Considerations

1. **Review scripts**: Always review any scripts included in skills before using them
2. **Use allowlists**: In sensitive environments, use the `allowed` configuration to restrict available skills
3. **Audit skill sources**: Only use skills from trusted sources

## Troubleshooting

### Skills Not Being Discovered

1. Check that the skill directory exists and contains a valid `SKILL.md` file
2. Verify the frontmatter has both `name` and `description` fields
3. Ensure the YAML frontmatter is properly formatted (starts and ends with `---`)
4. Check that skills are enabled in configuration (`skills.enabled: true`)

### Skill Not Being Invoked

1. Verify the skill description clearly indicates when it should be used
2. Check if the skill is in the allowlist (if configured)
3. Use `--no-skills` flag to confirm the behavior difference

### Debugging

Enable debug logging to see skill discovery and invocation:

```bash
KODELET_LOG_LEVEL=debug kodelet run "your query"
```

## Comparison: Skills vs Fragments

| Feature | Skills | Fragments/Recipes |
|---------|--------|-------------------|
| Invocation | Model-invoked (automatic) | User-invoked (explicit) |
| Purpose | Domain expertise | Task templates |
| Configuration | Name + Description | Name + Variables |
| Execution | Loads into context | Executed as prompt |
| Best for | Specialized knowledge | Repetitive tasks |

Use **skills** when you want Kodelet to automatically apply domain expertise.
Use **fragments** when you want to explicitly trigger a predefined workflow.
