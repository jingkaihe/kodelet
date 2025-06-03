# GitHub Actions Integration

Kodelet provides seamless GitHub Actions integration through the [kodelet-action](https://github.com/jingkaihe/kodelet-action), enabling automated software engineering tasks directly in your repository workflows.

## Overview

The Kodelet Action automates software engineering tasks using advanced AI models, including:

* 🤖 **AI-Powered Engineering**: Automates software engineering tasks using advanced AI models
* 📝 **Issue Resolution**: Automatically resolves GitHub issues with code changes and explanations
* 🔍 **PR Reviews**: Provides intelligent code review comments and suggestions  
* ⚡ **Background Processing**: Runs asynchronously without blocking your development workflow
* 🔄 **Multi-Event Support**: Works with issue comments, PR comments, and review comments
* 🛡️ **Secure**: Uses GitHub tokens and API keys securely through GitHub Secrets

## Quick Start

### 1. Setup API Key

Add your Anthropic API key to your repository secrets:

1. Go to your repository → Settings → Secrets and variables → Actions
2. Click "New repository secret"
3. Name: `ANTHROPIC_API_KEY`
4. Value: Your Anthropic API key (starts with `sk-ant-`)

### 2. Create Workflow File

Create `.github/workflows/kodelet.yml` in your repository:

```yaml
name: Background Kodelet

on:
  issue_comment:
    types: [created]
  issues:
    types: [opened, assigned]
  pull_request_review_comment:
    types: [created]
  pull_request_review:
    types: [submitted]

permissions:
  issues: write          # Comment on issues
  pull-requests: write   # Create PRs
  contents: write        # Push commits

env:
  TIMEOUT_MINUTES: "300"

jobs:
  background-kodelet:
    runs-on: ubuntu-latest
    timeout-minutes: 360  # 6 hours
    if: |
      (
        (github.event_name == 'issues' && contains(github.event.issue.body, '@kodelet')) ||
        (github.event_name == 'issue_comment' && contains(github.event.comment.body, '@kodelet')) ||
        (github.event_name == 'pull_request_review_comment' && contains(github.event.comment.body, '@kodelet')) ||
        (github.event_name == 'pull_request_review' && contains(github.event.review.body, '@kodelet'))
      ) &&
      (
        (github.event.issue.author_association == 'OWNER' || github.event.issue.author_association == 'MEMBER' || github.event.issue.author_association == 'COLLABORATOR') ||
        (github.event.comment.author_association == 'OWNER' || github.event.comment.author_association == 'MEMBER' || github.event.comment.author_association == 'COLLABORATOR') ||
        (github.event.review.author_association == 'OWNER' || github.event.review.author_association == 'MEMBER' || github.event.review.author_association == 'COLLABORATOR')
      )

    steps:
      - name: Checkout Repository
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
          token: ${{ secrets.GITHUB_TOKEN }}

      - name: Set up Go # as the dev env
        uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Run Kodelet
        uses: jingkaihe/kodelet-action@v0.1.2-alpha
        with:
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
          kodelet-version: 0.0.35.alpha
```

### 3. Trigger Kodelet

Comment `@kodelet` on any issue or pull request to trigger automated assistance:

- **Issues**: `@kodelet please fix this bug`
- **PRs**: `@kodelet review this code`
- **PR Reviews**: Include `@kodelet` in review comments

## Action Inputs

| Input | Description | Required | Default |
|-------|-------------|----------|---------|
| `anthropic-api-key` | Anthropic API key for Kodelet | ✅ | |
| `github-token` | GitHub token for repository operations | ❌ | `${{ github.token }}` |
| `commenter` | Username who triggered the action | ❌ | Auto-detected from event |
| `event-name` | GitHub event name | ❌ | `${{ github.event_name }}` |
| `issue-number` | Issue or PR number | ❌ | Auto-detected from event |
| `comment-id` | Comment ID (for issue comments on PRs) | ❌ | Auto-detected from event |
| `review-id` | Review ID (for PR review comments) | ❌ | Auto-detected from event |
| `repository` | Repository in format owner/repo | ❌ | `${{ github.repository }}` |
| `is-pr` | Whether this is a pull request | ❌ | Auto-detected from event |
| `pr-number` | Pull request number | ❌ | Auto-detected from event |
| `timeout-minutes` | Timeout for execution in minutes | ❌ | `300` |
| `log-level` | Log level (debug, info, warn, error) | ❌ | `info` |
| `kodelet-version` | Kodelet version to install (e.g., v0.0.35.alpha, latest) | ❌ | `latest` |

## Usage Examples

### Basic Usage (Minimal Configuration)

```yaml
- uses: jingkaihe/kodelet-action@v0.1.2-alpha
  with:
    anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
    # All other inputs are automatically populated from GitHub context
```

### Custom Configuration

```yaml
- uses: jingkaihe/kodelet-action@v0.1.2-alpha
  with:
    anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
    timeout-minutes: 180  # 3 hours
    log-level: debug
    kodelet-version: v0.0.35.alpha  # Pin to specific version
```

### Version Pinning

You can control which version of Kodelet is installed:

```yaml
# Use latest release (default)
- uses: jingkaihe/kodelet-action@v0.1.2-alpha
  with:
    kodelet-version: latest

# Pin to specific version
- uses: jingkaihe/kodelet-action@v0.1.2-alpha
  with:
    kodelet-version: 0.0.35.alpha
```

**Recommended approaches:**
- **Production**: Pin to a specific stable version for consistency
- **Development**: Use `latest` to get the newest features
- **Testing**: Pin to specific versions to ensure reproducible results

## Supported Events

| Event | Description | Kodelet Command |
|-------|-------------|-----------------|
| `issue_comment` | Comments on issues | `kodelet resolve --issue-url` |
| `issue_comment` (on PR) | Comments on pull requests | `kodelet pr-respond --pr-url --issue-comment-id` |
| `pull_request_review_comment` | Inline PR review comments | `kodelet pr-respond --pr-url --review-id` |
| `pull_request_review` | PR review submissions | `kodelet pr-respond --pr-url --review-id` |

## Workflow Trigger Conditions

The action only runs when:

1. **Event contains `@kodelet`**: The trigger event (comment, issue, review) must contain `@kodelet`
2. **Author has proper permissions**: Only users with `OWNER`, `MEMBER`, or `COLLABORATOR` association can trigger the action
3. **Supported event types**: Only specific GitHub events are supported (see table above)

### Trigger Examples

**Issue Comments:**
```
@kodelet please fix this bug by implementing proper error handling
```

**Pull Request Comments:**
```
@kodelet review this code and suggest improvements
```

**Pull Request Review Comments:**
```
This function looks complex. @kodelet can you refactor this?
```

## Permissions Required

The action requires the following GitHub permissions:

```yaml
permissions:
  issues: write          # Comment on issues
  pull-requests: write   # Comment on PRs and create PRs
  contents: write        # Push commits and create branches
```

## Security Considerations

- **API Keys**: Store your Anthropic API key in GitHub Secrets, never in code
- **GitHub Token**: Uses the automatically provided `GITHUB_TOKEN` with limited scope
- **Repository Access**: Only maintainers/collaborators can trigger the action
- **Timeout Protection**: Execution is limited by configurable timeout (default: 5 hours)

## Error Handling

The action automatically handles errors and posts informative comments when execution fails:

- API rate limits or service unavailability
- Complex requirements needing human intervention
- Environmental or dependency issues
- Timeout exceeded

Failed runs include links to workflow logs for debugging.

## Troubleshooting

### Common Issues

1. **Action not triggering**
   - Ensure `@kodelet` is included in the comment/issue
   - Check that the user has proper repository permissions
   - Verify the workflow file is in `.github/workflows/`

2. **API errors**
   - Verify `ANTHROPIC_API_KEY` is set correctly in repository secrets
   - Check API key has sufficient credits/quota

3. **Permission errors**
   - Ensure workflow has proper `permissions` section
   - Verify `GITHUB_TOKEN` has required scopes

4. **Timeouts**
   - Consider increasing `timeout-minutes` for complex tasks
   - Review workflow logs for specific timeout causes

### Debugging

Enable debug logging for more detailed output:

```yaml
- uses: jingkaihe/kodelet-action@v0.1.2-alpha
  with:
    anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
    log-level: debug
```

## Versioning

This action follows semantic versioning:

- **Latest stable**: `@v0`
- **Specific version**: `@v0.1.2-alpha`
- **Development**: `@main` (not recommended for production)

## Best Practices

1. **Pin versions in production**: Use specific version tags for stability
2. **Set appropriate timeouts**: Balance between allowing complex tasks and preventing runaway processes
3. **Monitor usage**: Keep track of API usage and costs
4. **Use descriptive comments**: Be specific about what you want Kodelet to do
5. **Review before merging**: Always review Kodelet's changes before merging

## Examples from Kodelet Repository

The Kodelet repository itself uses this action. See [`.github/workflows/kodelet-background.yml`](../.github/workflows/kodelet-background.yml) for a real-world example of the configuration in use.

## Support

- 📖 [Kodelet Documentation](https://github.com/jingkaihe/kodelet)
- 🐛 [Report Issues](https://github.com/jingkaihe/kodelet-action/issues)
- 💬 [Discussions](https://github.com/jingkaihe/kodelet-action/discussions)
- 🛍️ [GitHub Marketplace](https://github.com/marketplace/actions/kodelet-action)