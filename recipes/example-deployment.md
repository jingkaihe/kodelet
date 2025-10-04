---
name: Example Deployment Recipe
description: Demonstrates hybrid default values (YAML + template function)
defaults:
  branch: main
  env: development
  platform: linux/amd64
---

# Deployment Configuration

## Core Settings (from YAML defaults)
- Branch: {{.branch}}
- Environment: {{.env}}
- Platform: {{.platform}}

## Optional Settings (using template default function)
- Message: {{default .message "Standard deployment"}}
- Notify: {{default .notify "false"}}
- Build args: {{default .build_args "none"}}

## Deployment Instructions

Please create a deployment plan for:
- Branch: **{{.branch}}** to environment **{{.env}}**
- Platform: {{.platform}}
{{if ne (default .message "Standard deployment") "Standard deployment"}}
- Special instructions: {{.message}}
{{end}}
{{if eq (default .notify "false") "true"}}
- Send notifications after deployment
{{end}}

Include rollback procedures and health checks in your plan.
