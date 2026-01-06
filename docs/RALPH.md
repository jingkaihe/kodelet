# Ralph - Autonomous Feature Development Loop

Ralph is an outer loop harness for autonomous software development. It iteratively runs an AI agent to implement features from a PRD (Product Requirements Document), tracking progress between sessions.

## Overview

The Ralph pattern solves a fundamental challenge with long-running AI agents: they must work across multiple context windows with no memory between sessions. Ralph addresses this by:

1. **Using a structured PRD** (JSON format) that defines features to implement
2. **Tracking progress** in a dedicated file that persists between iterations
3. **Making git commits** to maintain clean, recoverable state
4. **Running type checks and tests** to verify each feature works

## Quick Start

```bash
# Initialize a new project with PRD and progress files
kodelet ralph init

# Or use the standalone recipe
kodelet run -r ralph-init --arg prd=features.json

# Or create your own prd.json file (see format below)

# Run the autonomous loop
kodelet ralph

# Run with custom settings
kodelet ralph --prd features.json --progress dev-log.txt --iterations 50

# Use a custom completion signal
kodelet ralph --signal DONE
```

## How It Works

### The Iteration Loop

Each iteration of Ralph:

1. **Reads the PRD** to understand all features and priorities
2. **Reads the progress file** to understand previous work
3. **Checks git history** to understand the current state
4. **Selects the highest-priority incomplete feature** to work on
5. **Implements the feature** with type checking and tests
6. **Updates the PRD** marking the feature as complete
7. **Appends to progress file** documenting what was done
8. **Makes a git commit** with descriptive message

### Completion Detection

The loop automatically exits when:
- All features in the PRD are marked as `"passes": true`
- The agent outputs `<promise>SIGNAL</promise>` (where SIGNAL is configurable via `--signal`, default: `COMPLETE`)
- Maximum iterations are reached

## PRD Format

The PRD is a JSON file with the following structure:

```json
{
  "name": "Project Name",
  "description": "Brief project description",
  "features": [
    {
      "id": "feature-1",
      "category": "functional",
      "priority": "high",
      "description": "Description of what the feature does",
      "steps": [
        "Step 1 to verify the feature works",
        "Step 2 to verify..."
      ],
      "passes": false
    },
    {
      "id": "feature-2",
      "category": "infrastructure",
      "priority": "medium",
      "description": "Another feature",
      "steps": ["Verification step"],
      "passes": false
    }
  ]
}
```

### Feature Fields

| Field | Description |
|-------|-------------|
| `id` | Unique identifier for the feature |
| `category` | Type: `functional`, `infrastructure`, `testing`, `documentation` |
| `priority` | Importance: `high`, `medium`, `low` |
| `description` | Clear description of what the feature does |
| `steps` | List of steps to verify the feature works |
| `passes` | Boolean - whether the feature is complete |

## Progress File

The progress file is a simple text log that tracks work across iterations:

```
# Progress Log

This file tracks progress across Ralph iterations.

---

## 2025-01-06 14:30 - Iteration 1
Feature: feature-1 (User Authentication)
- Implemented JWT token validation
- Added middleware for protected routes
- Tests passing: 12/12

Notes for next iteration:
- Need to implement refresh token logic
- Consider rate limiting for auth endpoints

---

## 2025-01-06 15:45 - Iteration 2
Feature: feature-2 (Refresh Tokens)
...
```

## Command Reference

### `kodelet ralph`

Run the autonomous development loop.

```bash
kodelet ralph [flags]

Flags:
  --prd string        Path to PRD JSON file (default "prd.json")
  --progress string   Path to progress file (default "progress.txt")
  --iterations int    Maximum iterations (default 10)
  --signal string     Completion signal keyword (default "COMPLETE")
```

### `kodelet ralph init`

Initialize a new PRD by analyzing the current repository.

```bash
kodelet ralph init [extra instructions] [flags]

Flags:
  --prd string        Path for PRD file to create (default "prd.json")
  --progress string   Path for progress file to create (default "progress.txt")
```

Examples:
```bash
# Basic initialization
kodelet ralph init

# With extra instructions pointing to design docs
kodelet ralph init "take a look at the design doc in ./design.md"

# Focus on specific areas
kodelet ralph init "focus on authentication and API features"

# Combine with custom PRD path
kodelet ralph init --prd features.json "see specs in ./docs/requirements.md"
```

## Shell Script Alternative

You can also use a shell script for more control:

```bash
#!/bin/bash
set -e

ITERATIONS=${1:-10}
SIGNAL=${2:-COMPLETE}
PRD=${3:-prd.json}
PROGRESS=${4:-progress.txt}

echo "Ralph Loop: PRD=$PRD, Progress=$PROGRESS, Signal=$SIGNAL"

for ((i=1; i<=ITERATIONS; i++)); do
  echo "Iteration $i of $ITERATIONS"
  echo "--------------------------------"
  result=$(kodelet run -r ralph --arg prd="$PRD" --arg progress="$PROGRESS" --arg signal="$SIGNAL")
  echo "$result"
  
  if [[ "$result" == *"<promise>$SIGNAL</promise>"* ]]; then
    echo "PRD complete after $i iterations!"
    exit 0
  fi
done

echo "Reached maximum iterations ($ITERATIONS). PRD may not be fully complete."
```

Usage:
```bash
./ralph.sh 20              # 20 iterations with defaults
./ralph.sh 50 DONE         # Custom signal
./ralph.sh 30 COMPLETE features.json dev-log.txt  # All custom
```

## Best Practices

### PRD Design

1. **Keep features atomic** - Each feature should be completable in one iteration
2. **Use clear verification steps** - The agent uses these to confirm completion
3. **Prioritize by dependencies** - High-priority features should be foundational
4. **Use JSON format** - Less likely to be modified incorrectly than Markdown

### Progress Tracking

1. **Review progress file regularly** - It's your window into the agent's work
2. **Git commits are recoverable** - Use `git revert` if something goes wrong
3. **Adjust iterations as needed** - Start small, increase if needed

### Running Effectively

1. **Start with `ralph init`** - Let the agent analyze your project first
2. **Review the generated PRD** - Edit priorities and descriptions as needed
3. **Monitor early iterations** - Verify the agent understands your project
4. **Use signals to stop** - Ctrl+C gracefully stops after current iteration

## References

- [Anthropic: Effective Harnesses for Long-Running Agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)
- [ghuntley.com/ralph](https://ghuntley.com/ralph/)

## Troubleshooting

### Agent doesn't find features

Ensure your PRD has features with `"passes": false`. The agent skips completed features.

### Iterations run out before completion

Increase `--iterations` or reduce the scope of your PRD. Complex features may need multiple iterations.

### Progress file gets corrupted

The progress file is append-only. If corrupted, you can recreate it from git history.

### Agent modifies wrong files

Add clearer feature descriptions and verification steps to guide the agent.
