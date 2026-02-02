---
name: Git Commit Message Generator
description: Generates meaningful commit messages based on staged git changes using conventional commits format
arguments:
  template:
    description: Custom template for the commit message
  short:
    description: Generate a short single-line commit message
---

{{/* Template variables: .template .short */}}

{{if .template}}Generate a commit message following this template for the following git diff:

<template>
{{.template}}
</template>

<git_diff>
{{bash "git" "diff" "--cached"}}
</git_diff>{{else}}{{if .short}}Generate a concise commit message following conventional commits format for the following git diff.

**Requirements:**
- Single line commit message only
- Short, descriptive title that summarizes the changes
- No bullet points or additional descriptions
- Do not wrap output in markdown code blocks

<git_diff>
{{bash "git" "diff" "--cached"}}
</git_diff>{{else}}Generate a concise commit message following conventional commits format for the following git diff.

**Requirements:**
- Short description as the title
- Bullet points that break down the changes (2-3 sentences per bullet point)
- Maintain accuracy and completeness of the git diff
- Do not wrap output in markdown code blocks

<git_diff>
{{bash "git" "diff" "--cached"}}
</git_diff>{{end}}{{end}}

