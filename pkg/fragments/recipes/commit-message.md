---
name: Commit Message Generator
description: Generates conventional commit messages by analyzing staged changes
allowed_tools:
  - "bash"
  - "file_read"
  - "grep_tool"
  - "glob_tool"
  - "thinking"
allowed_commands:
  - "git *"
---

Please analyze the current staged changes and generate an appropriate commit message following conventional commit format.

## Current Git Status:

<git-status>
{{bash "git" "status" "--porcelain"}}
</git-status>

## Staged Changes:

<git-diff-cached>
{{bash "git" "diff" "--cached"}}
</git-diff-cached>

## Task:

Based on the staged changes above, generate a conventional commit message that:

1. **Follows conventional commit format**: `type(scope): description`
2. **Uses appropriate type**:
   - `feat`: new feature
   - `fix`: bug fix
   - `docs`: documentation changes
   - `style`: code style changes (formatting, etc.)
   - `refactor`: code refactoring
   - `test`: adding or updating tests
   - `chore`: maintenance tasks
   - `ci`: CI/CD related changes
   - `perf`: performance improvements
   - `build`: build system or dependency changes
3. **Includes scope** when applicable (e.g., component, module, or file affected)
4. **Provides clear, concise description** (50 chars or less for the summary)
5. **Adds body** if needed for complex changes (wrap at 72 chars)
6. **Includes breaking change footer** if applicable

## Output Format:

**IMPORTANT**: The output should be the commit message ONLY. Do not wrap in markdown code blocks or add explanations unless specifically requested.

## Style Guidelines:
{{if .Context}}
{{.Context}}
{{else}}
- Default style: Title + bullet points for detailed changes
- For simple changes: Single descriptive line
- For complex changes: Title + body with bullet points explaining each major change
{{end}}

## Example formats:
- `feat(auth): add JWT token validation`
- `fix(api): handle null response in user endpoint`
- `docs(readme): update installation instructions`
- `refactor(utils): extract common validation logic`

Generate the commit message now: