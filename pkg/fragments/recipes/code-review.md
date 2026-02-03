---
name: Code Review
description: Performs a comprehensive code review of changes with focus on quality, security, and best practices
workflow: true
arguments:
  target:
    description: Target branch or commit to compare against
    default: "main"
  scope:
    description: Scope of review - 'staged' for staged changes, 'working' for working directory, 'branch' for branch comparison
    default: "staged"
  focus:
    description: Optional focus area for the review (e.g., correctness, security, architecture, performance, testing)
---

{{/* Template variables: .target .scope .focus */}}

Perform a comprehensive code review of the changes.

## Step 1: Gather the Changes

{{if eq .scope "staged"}}Review the staged changes:
- Run "git diff --cached" to get the staged changes
- Run "git diff --cached --stat" to understand the scope of changes{{else if eq .scope "working"}}Review the working directory changes:
- Run "git diff" to get the uncommitted changes
- Run "git diff --stat" to understand the scope of changes
- Run "git diff --cached" to also check staged changes{{else}}Review the branch changes compared to {{.target}}:
- Run "git fetch origin" to ensure up-to-date remote tracking
- Run "git diff {{.target}}...HEAD" to get the changes
- Run "git diff {{.target}}...HEAD --stat" to understand the scope of changes
- Run "git log --oneline {{.target}}...HEAD" to understand the commit history{{end}}

## Step 2: Understand the Context

Do not limit the review to the changes alone. Navigate the codebase to understand:
- How the changed code fits into the broader architecture
- What existing patterns and conventions are used in related code
- How other parts of the codebase interact with the changed components
- The purpose and responsibilities of the modules being modified

This context is essential for providing informed and thorough feedback.

## Step 3: Analyze and Review

For each changed file, analyze the following aspects:

### Code Quality and Correctness
- Readability and naming: Is the code clear and self-documenting with appropriate names?
- Maintainability: Is the code easy to maintain, extend, and not overly complex?
- Duplication: Is there code duplication that should be refactored?
- Logic and edge cases: Are there bugs, logical issues, or unhandled edge cases?
- Error and resource handling: Are errors handled and resources properly managed?

### Security
- Input validation: Is user input properly validated and sanitized?
- Authentication/Authorization: Are auth checks in place where needed?
- Sensitive data: Is sensitive data properly protected?
- Injection vulnerabilities: Are there SQL, command, or other injection risks?
- Dependencies: Are there any known vulnerable dependencies being added?

### Architecture and Design
- Patterns: Are appropriate design patterns used?
- Separation of concerns: Is the code properly modularized?
- Dependencies: Are dependencies between components reasonable?
- Extensibility: Is the design flexible for future changes?
- Consistency: Does the design align with existing architecture?

### Performance and Efficiency
- Algorithmic efficiency: Are there any obvious performance issues?
- N+1 queries: Are there database query patterns that could be optimized?
- Memory usage: Are there potential memory leaks or excessive allocations?
- Caching: Should any results be cached?

### Testing and Documentation
- Test coverage: Are the changes adequately tested?
- Test quality: Are the tests meaningful and maintainable?
- Edge cases: Do tests cover edge cases?
- Comments: Are complex parts of the code documented?
- API documentation: Are public APIs documented?
- README updates: Does the README need updating?

## Step 4: Provide Feedback

Structure your review as follows:

### Summary
Provide a brief overall assessment of the changes.

### Critical Issues
List any issues that MUST be fixed before merging (bugs, security issues, breaking changes).

### Suggestions
List recommended improvements that would significantly improve the code.

### Minor Comments
List optional improvements or style suggestions.

### Positive Feedback
Highlight any particularly good patterns or improvements noticed.

## Important Guidelines

- Be constructive and specific in feedback
- Explain WHY something is an issue, not just WHAT the issue is
- Provide concrete suggestions for fixes when possible
- Always include specific line references when citing code (e.g., "file.go:42")
- Consider the context and constraints of the project
- Focus on the most impactful issues first
- If no issues are found in a category, you can skip that category
{{if .focus}}
## Focus Area

Focus this review primarily:

{{.focus}}

While you may note critical issues in other areas, prioritize analysis related to the focus area.
{{end}}
