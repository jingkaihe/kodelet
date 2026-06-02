# Agentic skills

Skills are model-invoked capabilities. Unlike recipes, users normally do not call them directly; the model invokes the `skill` tool when relevant.

## Skill locations

- `./.kodelet/skills/<name>/SKILL.md` — repository-local standalone.
- `./.kodelet/plugins/<org@repo>/skills/<name>/SKILL.md` — repository-local plugin.
- `~/.kodelet/skills/<name>/SKILL.md` — user-global standalone.
- `~/.kodelet/plugins/<org@repo>/skills/<name>/SKILL.md` — user-global plugin.
- `skills/<name>/SKILL.md` — built-in/source-tree skills when packaged or available.

## Recommended structure

```text
my-skill/
  SKILL.md
  references/
    api.md
    troubleshooting.md
  examples/
    sample-input.txt
  scripts/
    helper.py
  templates/
    template.txt
```

Keep `SKILL.md` small and put optional details in supporting files. The skill loader injects `SKILL.md`; supporting files are read only when the agent chooses to inspect them.

Minimal `SKILL.md`:

```markdown
---
name: my-skill
description: Brief description for model decision-making
---

# My Skill

Use this skill for ...

Read `references/details.md` only when the task needs ...
```

## Configuration

```yaml
skills:
  enabled: true
  allowed:
    - pdf
    - xlsx
```

Disable skills for a run:

```bash
kodelet run --no-skills "query"
kodelet acp --no-skills
```

## Skill plugins

Plugins can bundle skills from GitHub repositories:

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

Plugin repository layout for skills:

```text
my-plugin-repo/
  skills/
    my-skill/
      SKILL.md
      references/
        api.md
```

Plugin skills are prefixed with `org/repo/` to avoid collisions.

