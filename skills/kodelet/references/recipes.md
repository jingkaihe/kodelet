# Recipes

## Agent context files

Kodelet automatically loads repository context from `AGENTS.md`. Include:

- Project overview and structure.
- Tech stack and dependency managers.
- Build, test, lint, and deploy commands.
- Coding style, error handling, logging, and review preferences.
- Important operational constraints.

Bootstrap:

```bash
kodelet run -r init
```

## Fragments/recipes

Recipes are user-invoked prompt templates. Store custom recipes in `./recipes/` or `~/.kodelet/recipes/`.

```bash
kodelet recipe list
kodelet recipe show init
kodelet run -r init
kodelet run -r my-recipe --arg project="Kodelet" --arg focus_area="security"
kodelet run -r my-recipe "additional context"
```

Recipe capabilities:

- Variable substitution: `{{.variable_name}}`.
- Bash substitution: `{{bash "git" "branch" "--show-current"}}`.
- Frontmatter arguments with descriptions/defaults.
- `allowed_tools` and `allowed_commands` restrictions.
Example:

```markdown
---
name: My Custom Recipe
description: Brief description
arguments:
  project:
    description: The project name to analyze
    default: "default-value"
  focus_area:
    description: Area to focus the analysis on
allowed_tools:
  - file_read
  - grep_tool
  - bash
allowed_commands:
  - "git *"
  - "cat *"
---

Current branch: {{bash "git" "branch" "--show-current"}}
Project: {{.project}}

Please analyze {{.focus_area}}.
```

## Recipe plugins

Plugins can bundle recipes from GitHub repositories:

```bash
kodelet plugin add orgname/repo
kodelet plugin add orgname/repo@v1.0.0
kodelet plugin add orgname/repo -g
kodelet plugin add orgname/repo --force

kodelet plugin list
kodelet plugin show orgname/repo
kodelet plugin remove orgname/repo
kodelet plugin remove orgname/repo -g
```

Plugin repository layout for recipes:

```text
my-plugin-repo/
  recipes/
    my-recipe.md
    workflows/
      deploy.md
```

Plugin recipes are prefixed with `org/repo/` to avoid collisions.
