---
name: Repository Initialization
description: Bootstrap AGENTS.md file with workspace context and conventions
---

Please thoroughly analyze this workspace and create an AGENTS.md file, which will provide kodelet the context to operate effectively in this workspace.

## What to Include in AGENTS.md

### Project Understanding
* **Project Overview**: Brief description of what the project does
* **Project Structure**: Key directories and their purposes (high-level organization, not exhaustive file listing)
* **Tech Stack**: Languages, frameworks, libraries, and tools used
* **Architecture**: High-level (big picture) architecture that helps navigate the codebase effectively

### Development Workflows
* **Key Commands**: Linting, testing, building, running, and other commands used repeatedly
* **Build System**: How the project is built and compiled
* **Testing**: How to run tests, test organization, and testing conventions
* **Dependencies**: How dependencies are managed

### Conventions and Style
* **Coding Style**: Language-specific conventions and preferences
* **Code Organization**: How code should be structured and organized
* **Naming Conventions**: File, class, function, and variable naming patterns
* **Error Handling**: Preferred error handling patterns
* **Logging**: Logging conventions and practices

### Integration Guidelines
* **Existing Rules**: If there are Cursor rules (in .cursor/rules/ or .cursorrules) or Copilot rules (in .github/copilot-instructions.md), incorporate the important parts
* **Engineering Principles**: Project-specific development practices and principles (not generic ones)

## What NOT to Include

* **Don't list every component**: Avoid exhaustive file structures that can be easily discovered
* **Don't include generic practices**: Skip universal development practices that aren't project-specific
* **Don't duplicate README**: Avoid repeating content from README.md - reference it instead
* **Don't make up content**: Only include information found in actual project files:
  - No invented "Common Development Tasks" unless expressly documented
  - No generic "Tips for Development" unless project-specific
  - No fabricated "Support and Documentation" sections

## If AGENTS.md Already Exists

* Review the current AGENTS.md file
* Analyze if it's missing important context or conventions
* Suggest specific improvements based on the workspace analysis
* Provide updated sections that enhance the existing documentation

## Analysis Process

1. Explore the repository structure
2. Identify build systems and configuration files
3. Review documentation files (README, CONTRIBUTING, etc.)
4. Check for existing agent/AI assistant rules
5. Analyze code style from actual code samples
6. Identify common patterns and conventions
7. Create or enhance AGENTS.md with actionable, project-specific context
