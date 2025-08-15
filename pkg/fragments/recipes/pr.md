---
name: GitHub Pull Request Generator
description: Creates pull requests with AI-generated title and description based on branch changes
---

{{/* Template variables: .target .template_file */}}

Create a pull request for the changes you have made on the current branch.

Please create a pull request following the steps below:

1. Make sure that the branch is up to date with the target branch. Push the branch to the remote repository if it is not already up to date.

2. To understand the current state of the branch, use parallel tool calling to perform the following checks:
  - Run "git status" to check the current status and any untracked files
  - Run "git diff" to check the changes to the working directory
  - Run "git diff --cached" to check the changes to the staging area
  - Run "git diff {{.target}}...HEAD" to understand the changes to the target branch
  - Run "git log --oneline {{.target}}...HEAD" to understand the commit history

3. Thoroughly review and analyse the changes, and wrap up your thoughts into the following sections:
- The category of the changes (chore, feat, fix, refactor, perf, test, style, docs, build, ci, revert)
- A summary of the changes as a title
- A detailed description of the changes based on the changes impact on the project
- Break down the changes into a few bullet points

4. Create a pull request against the target branch {{.target}}:
- **MUST USE** the 'mcp_create_pull_request' MCP tool if it is available in your tool list
- The 'mcp_create_pull_request' tool requires: owner, repo, title, body, head (current branch), base (target branch)
- Only use 'gh pr create ...' bash command as a last resort fallback if the MCP tool is not available

The body of the pull request should follow the following format:

<pr_body_format>
{{if .template_file}}{{bash "cat" .template_file}}{{else}}## Description
<high level summary of the changes>

## Changes
<changes in a few bullet points>

## Impact
<impact in a few bullet points>{{end}}
</pr_body_format>

IMPORTANT:
- After the parallel tool calls, when you performing the PR analysis, do not carry out extra tool calls to gather extra information, but instead use the information provided by the initial parallel analysis.
- Once you have created the PR, provide a link to the PR in your final response.
- !!!CRITICAL!!!: You should never update user's git config under any circumstances.