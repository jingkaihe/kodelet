## Release Note Generation

### Current Version Context:
Current version: {{bash "cat" "VERSION.txt"}}

### Git Status:
{{bash "git" "status" "--porcelain"}}

### Latest Release Tag:
{{bash "git" "describe" "--tags" "--abbrev=0" "HEAD^"}}

### Changes Since Last Release:
{{bash "sh" "-c" "PREV_TAG=$(git describe --tags --abbrev=0 HEAD^) && git log --oneline $PREV_TAG..HEAD"}}

### Detailed Diff Since Last Release:
{{bash "sh" "-c" "PREV_TAG=$(git describe --tags --abbrev=0 HEAD^) && git diff --stat $PREV_TAG..HEAD"}}

### File Changes:
{{bash "sh" "-c" "PREV_TAG=$(git describe --tags --abbrev=0 HEAD^) && git diff $PREV_TAG..HEAD"}}

## Task:
Based on the above git information, please analyze the changes since the previous release and write comprehensive release notes in RELEASE.md.

The release notes should:
- Be short, concise, and to the point
- Focus on user-facing changes and improvements
- Include any breaking changes prominently
- Group changes by category (Features, Bug Fixes, Internal Changes, etc.)
- Use conventional changelog format

Please update the RELEASE.md file with the new release notes for version {{bash "cat" "VERSION.txt"}}.
