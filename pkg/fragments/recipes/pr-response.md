---
name: Pull Request Response
description: Responds to pull request comments and reviews intelligently
allowed_tools:
  - "bash"
  - "file_read"
  - "file_write"
  - "file_edit"
  - "grep_tool"
  - "glob_tool"
  - "subagent"
  - "thinking"
  - "mcp_get_pr_comments"
  - "mcp_add_pr_comment"
  - "mcp_get_pr_review_comments"
allowed_commands:
  - "git *"
  - "gh *"
---

Please respond to the pull request {{.PullRequestURL}} following the appropriate workflow:

## Step 1: Analyze the Pull Request
1. Get the PR details, comments, and review comments
   - Preferrably use 'mcp_get_pr_comments' and 'mcp_get_pr_review_comments' if available
   - If not, use 'gh pr view {{.PullRequestURL}}' and 'gh pr view {{.PullRequestURL}} --comments' to get details
2. Review the PR description, changes, and any existing comments
3. Pay special attention to mentions of {{.BotMention}} - these are direct requests for action
4. Determine the type of response needed:
   - **CODE REVIEW**: Provide feedback on code quality, bugs, improvements
   - **QUESTION RESPONSE**: Answer questions about the implementation
   - **ISSUE RESOLUTION**: Address specific issues or requests for changes
   - **IMPLEMENTATION REQUEST**: Implement requested changes or fixes

## Step 2: Choose the Appropriate Response

### For CODE REVIEW:
1. Analyze the code changes in the PR
2. Look for:
   - Potential bugs or issues
   - Code quality improvements
   - Performance considerations
   - Security concerns
   - Best practices adherence
3. Provide constructive feedback with specific suggestions
4. Comment on the PR with your analysis

### For QUESTION RESPONSE:
1. Understand the specific questions being asked
2. Research the codebase to provide accurate answers
3. Provide clear, detailed explanations
4. Include code examples when helpful
5. Comment on the PR with your response

### For ISSUE RESOLUTION:
1. Identify the specific issues raised
2. Checkout the PR branch: "git fetch origin && git checkout {{.BranchName}}"
3. Make necessary changes to address the issues
4. Test the changes to ensure they work correctly
5. Commit the changes: "git add . && git commit -m 'fix: address PR feedback'"
6. Push the changes: "git push origin {{.BranchName}}"
7. Comment on the PR explaining what was fixed

### For IMPLEMENTATION REQUEST:
1. Understand what new functionality is being requested
2. Checkout the PR branch: "git fetch origin && git checkout {{.BranchName}}"
3. Implement the requested functionality
4. Add appropriate tests if needed
5. Update documentation if necessary
6. Commit the changes: "git add . && git commit -m 'feat: implement requested functionality'"
7. Push the changes: "git push origin {{.BranchName}}"
8. Comment on the PR explaining what was implemented

## Examples:

**CODE REVIEW Example:**
```
I've reviewed the changes and have a few suggestions:

1. Line 45: Consider adding null checks before accessing `user.email`
2. The `validateInput` function could benefit from more specific error messages
3. Great job on the comprehensive test coverage!

Overall, the implementation looks solid. Just address the null check issue and we should be good to go.
```

**QUESTION RESPONSE Example:**
```
Great question! The caching strategy works as follows:

1. We use Redis for session storage (expire after 24h)
2. Database queries are cached using the `@Cacheable` annotation
3. Cache invalidation happens automatically on data updates

Here's how to add caching to a new endpoint:
[code example]
```

**ISSUE RESOLUTION Example:**
```
I've addressed the feedback:

✅ Fixed the null pointer exception in user validation
✅ Added proper error handling for API timeouts  
✅ Updated tests to cover edge cases
✅ Improved variable naming for clarity

The changes are now ready for another review.
```

## Additional Context:
{{if .Context}}
{{.Context}}
{{end}}

IMPORTANT:
* Always be constructive and professional in feedback
* Focus on the code, not the person
* Provide specific, actionable suggestions
* If making code changes, ensure they don't break existing functionality
* Test your changes before pushing