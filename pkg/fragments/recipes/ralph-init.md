---
name: Ralph PRD Generator
description: Generate a PRD (Product Requirements Document) based on discussion and design docs for autonomous development
workflow: true
arguments:
  prd:
    description: Path to output the PRD (Product Requirements Document) JSON file
    default: "prd.json"
  progress:
    description: Path to output the progress log file
    default: "progress.txt"
---

{{/* Template variables: .prd .progress */}}

Based on our previous discussion and/or any design documents provided, create a PRD (Product Requirements Document) file at {{.prd}}.

Make sure that you have analyzed the codebase and identify features to implement.

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

## Guidelines

1. **Understand the Context**
   - Review any design documents or specifications provided in the conversation
   - Consider requirements discussed previously
   - Read README, AGENTS.md, and other project documentation
   - Analyze the project structure and architecture
   - Identify the tech stack and frameworks used

2. **Identify Features to Implement**
   - Extract features from design docs and discussion
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

Now create the PRD file based on the discussion, design docs, and/or repository analysis.
