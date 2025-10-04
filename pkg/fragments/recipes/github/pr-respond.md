---
name: GitHub PR Comment Responder
description: Intelligently responds to PR comments based on their type (code changes, questions, or code reviews)
defaults:
  bin: "kodelet"
  review_id: ""
  issue_comment_id: ""
---

{{/* Template variables: .pr_url .owner .repo .pr_number .review_id .issue_comment_id .bin */}}

Please respond to the PR comment {{.pr_url}} following the appropriate workflow based on the comment type:

<pr_basic_info>
{{bash "gh" "pr" "view" .pr_url "--json" "title,author,body,comments"}}
</pr_basic_info>

<git_diff>
{{bash "gh" "pr" "diff" .pr_url}}
</git_diff>

{{if ne .review_id ""}}
<pr_focused_comment>
	<pr_comment>
Review Comment ID {{.review_id}}:
{{$review_comment_path := print "repos/" .owner "/" .repo "/pulls/" .pr_number "/reviews/" .review_id}}{{bash "gh" "api" $review_comment_path "--jq" "{body: .body, author: .user.login, submitted_at: .submitted_at}"}}
	</pr_comment>

	<pr_discussions>
Related review discussions for comment {{.review_id}}:
{{$review_discussions_path := print "repos/" .owner "/" .repo "/pulls/" .pr_number "/reviews/" .review_id "/comments"}}{{bash "gh" "api" $review_discussions_path "--jq" "[.[] | {id: .id, author: .user.login, body: .body, line: .line, created_at: .created_at, diff_hunk: .diff_hunk}]"}}
	</pr_discussions>
</pr_focused_comment>
{{else if ne .issue_comment_id ""}}
<pr_focused_comment>
	<pr_comment>
Issue Comment ID {{.issue_comment_id}}:
{{$issue_comment_path := print "repos/" .owner "/" .repo "/issues/comments/" .issue_comment_id}}{{bash "gh" "api" $issue_comment_path "--jq" "{author: .user.login, body: .body, created_at: .created_at}"}}
	</pr_comment>

	<pr_discussions>
Issue comments don't have related discussions like review comments
	</pr_discussions>
</pr_focused_comment>
{{else}}
<pr_focused_comment>
	<pr_comment>
No specific comment ID provided. Please provide either --review-id or --issue-comment-id.
	</pr_comment>

	<pr_discussions>
Use the general PR context above to understand and respond to the comment.
	</pr_discussions>
</pr_focused_comment>
{{end}}

## Step 1: Analyze the Comment
1. Review the focused comment in the <pr_focused_comment> section above
2. Understand exactly what is being requested or asked
3. Determine the comment type:
   - **CODE CHANGE REQUEST**: Requires code modifications, bug fixes, implementation changes, refactoring, or testing updates
   - **QUESTION REQUEST**: Asks for clarification, explanation, discussion, or information about the code/architecture
   - **CODE REVIEW REQUEST**: Asks for code review, quality assessment, security analysis, or best practices evaluation

## Step 2: Choose the Appropriate Workflow

### For CODE CHANGE REQUESTS (Implementation/Fix/Update):
1. Check the current state of the PR branch:
   - Use "git checkout <pr-branch>" to switch to the PR branch
   - Run "git pull origin <pr-branch>" to ensure latest changes
   - Check current working directory state

2. Analyze the specific change request:
   - Review the comment details to understand exactly what changes are needed
   - Create a focused todo list for the specific request
   - If the request is unclear, ask for clarification in your comment response, do not implement changes

3. Implement the specific changes:
   - Focus only on what was requested in the comment
   - Make precise, targeted changes to address the feedback
   - Avoid scope creep or unrelated improvements
   - Make sure that you run 'git add ...' to add the changes to the staging area before you commit

4. Finalize the changes:
   - Ask subagent to run "{{.bin}} commit --short --no-confirm" to commit changes
   - Push updates with "git push origin <pr-branch>"
   - Reply to the specific comment with a summary of actions taken using "gh pr comment <pr-number> --body <summary>"

### For QUESTION REQUESTS (Clarification/Discussion):
1. Understand the question by analyzing the PR context and codebase
2. Research relevant code, documentation, and architecture to gather information
3. Provide a comprehensive answer that addresses the question directly
4. Reply to the specific comment with your detailed explanation using "gh pr comment <pr-number> --body <answer>"
5. Do NOT make code changes, commits, or push updates for questions

### For CODE REVIEW REQUESTS (Review/Quality Assessment):
1. Analyze the PR code changes and identify areas for review:
   - Use subagent to examine the codebase and understand the changes
   - Review the git diff to understand what was modified
   - Check for code quality, security, performance, and best practices issues

2. Conduct comprehensive code review:
   - Look for potential bugs, security vulnerabilities, or logic errors
   - Check for adherence to coding standards and best practices
   - Evaluate code maintainability, readability, and performance
   - Assess test coverage and documentation quality
   - Review for potential side effects or breaking changes

3. Create and submit proper GitHub review using MCP tools:
   - First, create a pending review using 'mcp_create_pending_pull_request_review'
   - Add specific review comments on code lines using 'mcp_add_pull_request_review_comment_to_pending_review'
   - Organize findings by category (security, performance, style, etc.)
   - Include specific line references and code examples where applicable
   - Suggest concrete improvements or alternatives
   - Highlight both positive aspects and areas for improvement
   - Prioritize issues by severity (critical, major, minor)
   - Finally, submit the review using 'mcp_submit_pending_pull_request_review' with event "COMMENT"

4. Do NOT make code changes, commits, or push updates for code reviews - only provide feedback through GitHub review system

## Tool Usage Guidelines:

### For CODE CHANGE REQUESTS - Use Standard Git/GitHub Tools:
- 'git checkout', 'git pull', 'git add', 'git push' for branch management
- '{{.bin}} commit --short --no-confirm' for committing changes
- 'gh pr comment <pr-number> --body <summary>' for responding with change summary

### For QUESTION REQUESTS - Use GitHub CLI:
- 'gh pr comment <pr-number> --body <answer>' for providing explanations
- Use subagent for codebase analysis and research

### For CODE REVIEW REQUESTS - Use MCP Tools:
- 'mcp_create_pending_pull_request_review' to start a review
- 'mcp_add_pull_request_review_comment_to_pending_review' to add line-specific comments
- 'mcp_submit_pending_pull_request_review' with event "COMMENT" to submit the review
- Use subagent for comprehensive code analysis

## Examples:

**CODE CHANGE REQUEST Example:**
<example>
Comment: "The error handling in lines 45-50 should use our custom error wrapper instead of the standard library errors"
This requires code modification -> Use CODE CHANGE workflow
</example>

**QUESTION REQUEST Example:**
<example>
Comment: "Can you explain why you chose this approach over using channels here?"
This asks for explanation -> Use QUESTION workflow
</example>

**CODE CHANGE REQUEST Example:**
<example>
Comment: "Please add unit tests for the new authentication function and fix the linting issues"
This requires code implementation -> Use CODE CHANGE workflow
</example>

**QUESTION REQUEST Example:**
<example>
Comment: "How does this change affect the existing database migrations? Will there be any backward compatibility issues?"
This asks for clarification -> Use QUESTION workflow
</example>

**CODE CHANGE REQUEST Example:**
<example>
Comment: "The timeout value should be configurable through environment variables instead of hardcoded"
This requires refactoring -> Use CODE CHANGE workflow
</example>

**QUESTION REQUEST Example:**
<example>
Comment: "What's the performance impact of this change compared to the previous implementation?"
This asks for information -> Use QUESTION workflow
</example>

**CODE REVIEW REQUEST Example:**
<example>
Comment: "Can you please review this code for security vulnerabilities and best practices?"
This asks for code review -> Use CODE REVIEW workflow
</example>

**CODE REVIEW REQUEST Example:**
<example>
Comment: "Please do a thorough code review of the authentication logic and check for potential issues"
This asks for code review -> Use CODE REVIEW workflow
</example>

**CODE REVIEW REQUEST Example:**
<example>
Comment: "Could you review this implementation for performance bottlenecks and suggest optimizations?"
This asks for code review -> Use CODE REVIEW workflow
</example>

IMPORTANT:
- !!!CRITICAL!!!: Never update user's git config under any circumstances
- Use a checklist to keep track of progress
- Focus ONLY on the specific comment - don't address other feedback in the same response
- For questions, provide thorough, helpful explanations without making code changes
- For code changes, follow the full development workflow with proper commits and responses
- Always acknowledge the specific comment you're responding to in your reply
- Keep comment responses concise but informative
