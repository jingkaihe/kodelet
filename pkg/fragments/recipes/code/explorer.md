---
name: Code Explorer
description: Explores and explains a codebase to help understand how it works
workflow: true
arguments:
  path:
    description: Absolute path to explore (directory or file). Defaults to current directory
  focus:
    description: What to understand (e.g., "authentication flow", "data model", "API structure", "error handling")
---

Explore and explain the codebase.

## Step 1: Discovery

Analyze {{default .path (bash "pwd")}}:
- Project structure and organization
- Entry points and main components
- Configuration and dependencies
- Relevant documentation

## Step 2: Understand Architecture

Map the high-level design related to the focus area:
- Component relationships and boundaries
- Design patterns in use
- Data flow through the system

## Step 3: Dig into Implementation

Trace through the actual code:
- Key files and their responsibilities
- Important functions and their logic
- Edge cases and error handling
- Go as deep as needed to fully understand

## Step 4: Create a Learning Journey

Design a guided path for understanding the code.

## Output Format

### Summary
2-3 sentence overview of what was found.

### Architecture Overview
How the relevant components fit together.

### Learning Journey

Provide a numbered sequence of files/concepts to read in order:

1. **Start here**: [file:line] - Why this is the entry point
2. **Then read**: [file:line] - What this builds on
3. **Next**: [file:line] - How this connects
4. ...continue until the reader has full understanding

For each step, explain:
- What to focus on in this file
- How it connects to the previous step
- What concept or pattern to learn here

### Key Concepts
Essential abstractions the reader must understand, with code references.

### Gotchas
Non-obvious details or potential pitfalls.

## Guidelines

- Design the journey from simple to complex
- Each step should build on previous understanding
- Be specific with file paths and line references
- Include relevant code snippets
- Explain the "why" not just the "what"

## Focus Area

Focus the exploration on: **{{.focus}}**
