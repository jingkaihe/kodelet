---
name: Ralph PRD Generator
description: Analyze a repository and generate a PRD (Product Requirements Document) for autonomous development
defaults:
  prd: "prd.json"
  progress: "progress.txt"
---

{{/* Template variables: .prd .progress */}}

Analyze this repository and create a PRD (Product Requirements Document) file at {{.prd}}.

## PRD Structure

The PRD should be a JSON file with the following structure:

```json
{
  "name": "Project Name",
  "description": "Brief project description",
  "features": [
    {
      "id": "feature-1",
      "category": "functional|infrastructure|testing|documentation",
      "priority": "high|medium|low",
      "description": "What the feature does",
      "steps": [
        "Step 1 to verify the feature works",
        "Step 2..."
      ],
      "passes": false
    }
  ]
}
```

## Analysis Guidelines

1. **Understand the Project**
   - Read README, AGENTS.md, and other documentation
   - Analyze the project structure and architecture
   - Identify the tech stack and frameworks used

2. **Identify Features to Implement**
   - Look for TODOs, FIXMEs, and incomplete implementations
   - Check for missing tests or low coverage areas
   - Identify potential improvements or optimizations
   - Look at open issues if available

3. **Prioritize Features**
   - **High priority**: Core functionality, blocking issues, security fixes
   - **Medium priority**: Important improvements, test coverage, refactoring
   - **Low priority**: Nice-to-haves, documentation, minor optimizations

4. **Feature Categories**
   - **functional**: Core features, business logic, user-facing functionality
   - **infrastructure**: Build system, CI/CD, deployment, configuration
   - **testing**: Unit tests, integration tests, E2E tests
   - **documentation**: README, API docs, code comments, guides

5. **Feature Design**
   - Each feature should be atomic and completable in one session
   - Include clear, testable verification steps
   - Consider dependencies between features
   - Mark all features as `"passes": false` initially

## Progress File

Also create an empty progress file at {{.progress}} with the following header:

```
# Progress Log

This file tracks progress across Ralph iterations.

---

```

## Output

Focus on actionable, specific features rather than vague improvements. Each feature should have:
- A unique, descriptive ID
- Clear description of what needs to be done
- Specific steps to verify completion
- Appropriate priority based on impact and dependencies

Now analyze the repository and create the PRD file.
