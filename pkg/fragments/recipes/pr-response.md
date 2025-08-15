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
  - "mcp_create_pending_pull_request_review"
  - "mcp_add_pull_request_review_comment_to_pending_review"
  - "mcp_submit_pending_pull_request_review"
allowed_commands:
  - "git *"
  - "gh *"
---

Please respond to the pull request {{.PullRequestURL}} following the appropriate workflow:

{{if .Context}}
## Context Information:

{{.Context}}

{{end}}
## Step 1: Analyze the Request

1. Review the PR details and focused comment
2. Understand exactly what is being requested or asked
3. Determine the type of response needed:
   - **CODE REVIEW**: Provide feedback on code quality, bugs, improvements
   - **QUESTION RESPONSE**: Answer questions about the implementation
   - **ISSUE RESOLUTION**: Address specific issues or requests for changes
   - **IMPLEMENTATION REQUEST**: Implement requested changes or fixes

## Step 2: Choose the Appropriate Response

### For CODE REVIEW:
1. Analyze the code changes in the PR using subagent
2. Look for potential issues: bugs, security, performance, best practices
3. Create a comprehensive GitHub review:
   - Use 'mcp_create_pending_pull_request_review' to start
   - Add line-specific comments with 'mcp_add_pull_request_review_comment_to_pending_review'
   - Submit with 'mcp_submit_pending_pull_request_review' (event: "COMMENT")
4. Organize findings by category and severity
5. Provide specific, actionable suggestions

### For QUESTION RESPONSE:
1. Research the codebase to understand the question
2. Provide clear, detailed explanations with examples
3. Comment on the PR with your response using 'gh pr comment'
4. Do NOT make code changes

### For ISSUE RESOLUTION:
1. Identify the specific issues raised
2. Checkout the PR branch: "git fetch origin && git checkout {{.BranchName}}"
3. Make targeted changes to address the feedback
4. Test changes and ensure they work correctly
5. Commit: "git add . && {{.BinPath}} commit --short --no-confirm"
6. Push: "git push origin {{.BranchName}}"
7. Comment explaining what was fixed

### For IMPLEMENTATION REQUEST:
1. Understand the requested functionality
2. Checkout PR branch: "git fetch origin && git checkout {{.BranchName}}"
3. Implement the requested features
4. Add tests and update documentation if needed
5. Commit: "git add . && {{.BinPath}} commit --short --no-confirm"
6. Push: "git push origin {{.BranchName}}"
7. Comment explaining what was implemented

## Tool Usage Guidelines:

- **Code Changes**: Standard git commands + GitHub CLI for comments
- **Questions**: GitHub CLI for responses + subagent for research
- **Code Reviews**: MCP tools for structured GitHub reviews + subagent for analysis

## Response Examples:

**Code Review:**
```
I've reviewed the changes and have suggestions:

1. Line 45: Add null checks before accessing `user.email`
2. Consider more specific error messages in `validateInput`
3. Excellent test coverage!

Overall solid implementation. Address the null check and we're good to go.
```

**Question Response:**
```
Great question! The caching works as follows:

1. Redis for sessions (24h expiry)
2. Database queries cached with `@Cacheable`
3. Auto-invalidation on updates

Here's how to add caching to new endpoints: [example]
```

**Issue Resolution:**
```
✅ Fixed null pointer in user validation
✅ Added timeout error handling  
✅ Updated tests for edge cases
✅ Improved variable naming

Ready for re-review!
```

## Important Notes:
- Focus on the specific comment/request only
- Be constructive and professional
- Provide actionable suggestions
- Test changes before pushing
- Never update git config