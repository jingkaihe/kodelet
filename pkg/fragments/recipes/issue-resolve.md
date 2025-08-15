---
name: Issue Resolver
description: Intelligently resolves GitHub issues based on their type (implementation vs question)
allowed_tools:
  - "bash"
  - "file_read"
  - "file_write"
  - "file_edit"
  - "grep_tool"
  - "glob_tool"
  - "subagent"
  - "todo_write"
  - "todo_read"
  - "thinking"
  - "mcp_get_issue_comments"
  - "mcp_add_issue_comment"
allowed_commands:
  - "git *"
  - "gh *"
---

Please resolve the github issue {{.IssueURL}} following the appropriate workflow based on the issue type:

## Step 1: Analyze the Issue
1. Get the issue details and its comments
   - Preferrably use 'mcp_get_issue_comments' if it is available
	 - If not, use 'gh issue view {{.IssueURL}}' and 'gh issue view {{.IssueURL}} --comments' to get the issue details and its comments.
2. Review the issue details and understand the issue.
3. Pay special attention to the latest comment with {{.BotMention}} - this is the instruction from the user.
4. Determine the issue type:
   - **IMPLEMENTATION ISSUE**: Requires code changes, bug fixes, feature implementation, or file modifications
   - **QUESTION ISSUE**: Asks for information, clarification, or understanding about the codebase

## Step 2: Choose the Appropriate Workflow

### For IMPLEMENTATION ISSUES (Feature/Fix/Code Changes):
1. Extract the issue number from the issue URL for branch naming
2. Create and checkout a new branch: "git checkout -b kodelet/issue-${ISSUE_NUMBER}-${BRANCH_NAME}"
3. Work on the issue:
   - Think step by step before starting
   - Add extra steps to the todo list for complex issues
   - Do not commit during this step
	 - Make sure that you run 'git add ...' to add the changes to the staging area before you commit.
4. Once resolved, use subagent to run "{{.BinPath}} commit --short --no-confirm" to commit changes
5. Use subagent to run "{{.BinPath}} pr" (60s timeout) to create a pull request
6. Comment on the issue with the PR link
   - Preferrably use 'mcp_add_issue_comment' if it is available
	 - If not, use 'gh issue comment ...' to comment on the issue.

### For QUESTION ISSUES (Information/Clarification):
1. Understand the question by reading issue comments and analyzing the codebase
2. Research the codebase to gather relevant information to answer the question
3. Once you have a comprehensive understanding, comment directly on the issue with your answer
4. Do NOT create branches, make code changes, or create pull requests

## Examples:

**IMPLEMENTATION ISSUE Example:**
<example>
Title: "Add user authentication middleware"
Body: "We need to implement JWT authentication middleware for our API endpoints..."
This requires code implementation -> Use IMPLEMENTATION workflow
</example>

**QUESTION ISSUE Example:**
<example>
Title: "How does the logging system work?"
Body: "Can someone explain how our current logging implementation handles different log levels..."
This asks for information -> Use QUESTION workflow
</example>

**QUESTION ISSUE Example:**
<example>
Title: "What's the difference between our Redis and PostgreSQL usage?"
Body: "@kodelet can you explain how we use Redis vs PostgreSQL in our architecture..."
This asks for clarification -> Use QUESTION workflow
</example>

**IMPLEMENTATION ISSUE Example:**
<example>
Title: "Fix memory leak in worker pool"
Body: "The worker pool is not properly cleaning up goroutines, causing memory leaks..."
This requires bug fix -> Use IMPLEMENTATION workflow
</example>

IMPORTANT:
* !!!CRITICAL!!!: Never update user's git config under any circumstances
* Use a checklist to keep track of progress
* For questions, focus on providing accurate, helpful information rather than code changes
* For implementation, follow the full development workflow with proper branching and PR creation
