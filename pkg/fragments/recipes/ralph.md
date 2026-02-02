---
name: Ralph - Autonomous Feature Development Loop
description: Iteratively work through a feature list, implementing one feature at a time with progress tracking
arguments:
  prd:
    description: Path to the PRD (Product Requirements Document) JSON file
    default: "prd.json"
  progress:
    description: Path to the progress log file
    default: "progress.txt"
  signal:
    description: Signal word to indicate completion
    default: "COMPLETE"
---

{{/* Template variables: .prd .progress .signal */}}

You are operating in an autonomous development loop. Your task is to implement features from a PRD (Product Requirements Document) one at a time.

## Context Files

### PRD (Feature List)
File: {{.prd}}

### Progress Log
File: {{.progress}}

## Your Task

1. **Read the PRD file** to understand all features and their priorities
2. **Read the progress file** to understand what has been done in previous iterations
3. **Read recent git history** to understand the current state of the codebase

## Work Instructions

### Feature Selection
- Find the highest-priority feature that is NOT yet marked as complete
- This should be the one YOU decide has the highest priority based on dependencies and impact
- Work on ONLY ONE FEATURE in this iteration

### Implementation Process
1. Implement the selected feature thoroughly
2. Ensure types check correctly (run appropriate type checking commands for the project)
3. Ensure tests pass (run the test suite for the project)
4. If tests fail, fix them before proceeding

### After Implementation
1. **Update the PRD**: Mark the completed feature as `"passes": true` in the JSON
2. **Append to progress file**: Add a timestamped entry describing:
   - Which feature was implemented
   - What changes were made
   - Any issues encountered and how they were resolved
   - Notes for the next iteration
3. **Make a git commit**: Commit your changes with a descriptive message

## Completion Signal

When you notice that ALL features in the PRD are complete (all have `"passes": true`), output:

```
<promise>{{.signal}}</promise>
```

This signals that the entire PRD has been implemented.

## Important Guidelines

- **ONE FEATURE AT A TIME**: Do not try to implement multiple features
- **LEAVE CODE MERGE-READY**: No major bugs, code should be orderly and well-documented
- **TRACK EVERYTHING**: The progress file is your memory between iterations
- **TEST THOROUGHLY**: Verify features work as expected before marking complete
- **INCREMENTAL COMMITS**: Each feature should be a separate, atomic commit

## PRD Format Reference

The PRD should be a JSON file with features structured like:
```json
{
  "features": [
    {
      "id": "feature-1",
      "category": "functional",
      "priority": "high",
      "description": "Description of the feature",
      "steps": [
        "Step 1",
        "Step 2"
      ],
      "passes": false
    }
  ]
}
```

Now, read the PRD and progress files, then implement the next highest-priority incomplete feature.
