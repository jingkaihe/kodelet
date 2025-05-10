# ADR 002: Git Commit Command for Kodelet

## Status

Proposed

## Context

Kodelet is designed to assist with software engineering and production operations tasks. Git is a fundamental tool in this domain, and crafting meaningful commit messages is often a time-consuming task that requires careful consideration of changes made.

Developers frequently need to create well-formatted, signed git commits that accurately describe the changes in their codebase. This process can be improved by leveraging Kodelet's AI capabilities to analyze git diffs and automatically generate appropriate commit messages.

## Decision

We will implement a new `kodelet commit` command that will:
1. Analyze the current git diff in the repository
2. Generate a meaningful commit message based on the changes
3. Create a signed git commit with the generated message

## Details

The implementation will include:

1. **Command Structure**:
   - Primary command: `kodelet commit`
   - Optional flags for customization (e.g., `--sign`, `--template`)

2. **Core Functionality**:
   - Retrieve the git diff using `git diff --cached` (only examining staged changes)
   - Process the diff using the existing `ask` function in cmd/kodelet/main.go
   - Generate a commit message following conventional commit format
   - Execute the git commit command with appropriate signing options
   - Add co-authorship attribution with `Co-authored-by: Kodelet <kodelet@tryopsmate.ai>`

3. **User Experience Features**:
   - Interactive confirmation before committing
   - Support for customizing the commit message before finalizing
   - Integration with existing git signing configurations
   - Clear indication that git staging (git add) must be performed before using this command

4. **Technical Implementation**:
   - Add a new subcommand to the Cobra command structure
   - Leverage the existing `ask` function for generating commit messages based on diffs
   - Preserve all git configurations without modification
   - Handle errors gracefully with meaningful messages
   - Implement co-authorship without changing git configuration

## Consequences

### Advantages

- Automates the creation of meaningful and consistent commit messages
- Saves developer time and cognitive effort
- Improves commit message quality and consistency
- Integrates smoothly with existing git workflows
- Reinforces Kodelet's role as a developer productivity tool

### Challenges

- Requires access to git commands and repository state
- May need additional permissions for git operations
- Quality of generated commit messages depends on AI model capabilities
- Must handle various git configurations and edge cases
- Requires changes to be staged (git add) before running the command
- Must maintain user's git configuration without modifications
- Need to implement co-authorship without altering git config

### Alternatives Considered

1. **Separate tool**: Creating a standalone tool for commit generation would require duplicating Kodelet's AI integration
2. **Git hook integration**: Pre-commit hooks could generate messages but would be less interactive
3. **Editor plugin**: Would require maintaining separate plugins for different editors

## Implementation Plan

1. Create a new Cobra command for the commit functionality
2. Implement the git diff retrieval mechanism using `git diff --cached`
3. Integrate with the existing `ask` function in cmd/kodelet/main.go for commit message generation
4. Add co-authorship functionality with `Co-authored-by: Kodelet <kodelet@tryopsmate.ai>`
5. Add commit message creation and git commit execution without modifying git config
6. Implement interactive confirmation and customization
7. Add clear user guidance about staging changes before using the command
8. Add documentation and examples
9. Test with various repository states and configurations

## References

- [Conventional Commits Specification](https://www.conventionalcommits.org/)
- [Git Documentation - git-commit](https://git-scm.com/docs/git-commit)
- [Git Documentation - git-diff](https://git-scm.com/docs/git-diff)
- [Anthropic Claude Documentation](https://docs.anthropic.com/)
