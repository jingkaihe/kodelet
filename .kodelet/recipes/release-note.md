---
name: Release Note Generator
description: Generates comprehensive release notes by analyzing git changes since the previous release
workflow: true
---

## Release Note Generation

### Current Version Context:

Current version: {{bash "cat" "VERSION.txt"}}

### Git Status:

<git-status>
{{bash "git" "status" "--porcelain"}}
</git-status>

### Latest Release Tag:

```
{{bash "git" "describe" "--tags" "--abbrev=0" "HEAD^"}}
```

### Changes Since Last Release:

<git-log>
{{bash "sh" "-c" "PREV_TAG=$(git describe --tags --abbrev=0 HEAD^) && git log --oneline $PREV_TAG..HEAD"}}
</git-log>

### Detailed Diff Since Last Release:

<diff-stat>
{{bash "sh" "-c" "PREV_TAG=$(git describe --tags --abbrev=0 HEAD^) && git diff --stat $PREV_TAG..HEAD"}}
</diff-stat>

### File Changes:

<diff>
{{bash "sh" "-c" "PREV_TAG=$(git describe --tags --abbrev=0 HEAD^) && git diff $PREV_TAG..HEAD"}}
</diff>

## Task:
Based on the above git information, please analyze the changes since the previous release and write comprehensive release notes in RELEASE.md.

Before writing the release note, please read the first 100 lines of the release notes to understand the tone and style of the existing notes

The release notes should:
- Be short, concise, and to the point
- Focus on user-facing changes and improvements
- Include any breaking changes prominently
- Group changes by category (Features, Bug Fixes, Internal Changes, etc.)
- Use conventional changelog format

Please update the RELEASE.md file with the new release notes for version {{bash "cat" "VERSION.txt"}}.
