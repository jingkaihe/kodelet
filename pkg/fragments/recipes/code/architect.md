---
name: architect
description: Analyzes codebase patterns and designs architectural solutions with implementation blueprints
workflow: true
arguments:
  path:
    description: Absolute path to analyze (directory or file). Defaults to current directory
  focus:
    description: What to design or improve (e.g., "add caching layer", "refactor authentication", "implement event sourcing")
---

{{/* Template variables: .path .focus */}}

Design an architectural solution for: **{{.focus}}**

## Phase 1: Pattern and Codebase Analysis

Analyze {{default .path (bash "pwd")}} to understand:
- Project structure and organization
- Existing architectural patterns and conventions
- Dependencies and component boundaries
- Constraints that will influence the design

## Phase 2: Architectural Design

Propose a design that fits with existing patterns:
- High-level approach and component relationships
- New/modified components with their interfaces
- Integration points with existing code

## Phase 3: Implementation Blueprint

Provide a concrete implementation plan:
- Files to create/modify with descriptions
- Ordered implementation steps with complexity estimates
- Key interface and type definitions
- Testing approach

## Output Format (ADR Style)

### Title
ADR-NNN: [Descriptive title for the architectural decision]

### Status
Proposed

### Context
What is the issue? Why does this decision need to be made? Include key findings from codebase analysis.

### Decision
**State the architectural decision assertively.** Be specific about what will be done, not what could be done.

### Consequences
What are the results of this decision? Include both positive outcomes and tradeoffs accepted.

### Implementation Blueprint
Concrete steps, file changes, and code outlines to execute the decision.

## Guidelines

- **Be assertive**: Make clear architectural choices. Do not present multiple options without a recommendation.
- Ground proposals in actual codebase structure
- Follow existing patterns unless there's compelling reason not to
- Be specific with file paths, function names, and code snippets
- Propose incremental changes over big-bang rewrites
