---
name: Custom Tool Generator
description: Creates a custom tool for Kodelet, implementing the Kodelet custom tool protocol
workflow: true
arguments:
  task:
    description: Description of what the custom tool should do
  global:
    description: Whether to save the tool globally (true) or locally (false)
    default: "false"
---

{{/* Template variables: .task, .global */}}
{{/* Configuration variables: .custom_tools_local_dir, .custom_tools_global_dir */}}

Create a custom tool that {{.task}}.

{{if eq .global "true"}}The tool will be saved to the **global** custom tools directory ({{.custom_tools_global_dir}}) and will be available across all projects.{{else}}The tool will be saved to the **local** custom tools directory ({{.custom_tools_local_dir}}) and will only be available in this project.{{end}}

Choose one of the following implementation approaches:
## Python Implementation (using uv)

Create the tool as a Python script using uv for dependency management:

1. **File Location**: Save as `{{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/[task_in_snake_case]` (e.g., "analyze log files" → `{{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/analyze_log_files`)
2. **Make it executable**: `chmod +x {{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/[task_in_snake_case]`
3. **Use proper shebang**: `#!/usr/bin/env uv` (automatically handles dependencies via inline script metadata)

### Python Template:

```python
#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.8"
# dependencies = [
#     "requests",
#     "httpx",
# ]
# ///

import json
import sys
from pathlib import Path

def get_description():
    """Return the tool description and schema."""
    return {
        "name": "[task_in_snake_case]",
        "description": "{{.task}}",
        "input_schema": {
            "type": "object",
            "properties": {
                # Add your parameters here based on the task
                "input": {
                    "type": "string",
                    "description": "Input data for the task"
                }
            },
            "required": ["input"]
        }
    }

def run_tool():
    """Execute the main tool functionality."""
    try:
        # Read JSON input from stdin
        input_data = json.load(sys.stdin)

        # Implement your tool logic here based on the task: {{.task}}

        # Example implementation:
        result = {
            "status": "success",
            "result": f"Processed: {input_data.get('input', '')}"
        }

        print(json.dumps(result, indent=2))

    except json.JSONDecodeError as e:
        error_response = {"error": f"Invalid JSON input: {str(e)}"}
        print(json.dumps(error_response))
        sys.exit(1)
    except Exception as e:
        error_response = {"error": f"Tool execution failed: {str(e)}"}
        print(json.dumps(error_response))
        sys.exit(1)

def main():
    if len(sys.argv) != 2:
        print("Usage: [task_in_snake_case] [description|run]", file=sys.stderr)
        sys.exit(1)

    command = sys.argv[1]

    if command == "description":
        print(json.dumps(get_description(), indent=2))
    elif command == "run":
        run_tool()
    else:
        print(f"Unknown command: {command}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
```


## Bash Implementation

Create the tool as a Bash script:

1. **File Location**: Save as `{{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/[task_in_snake_case]` (e.g., "analyze log files" → `{{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/analyze_log_files`)
2. **Make it executable**: `chmod +x {{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/[task_in_snake_case]`
3. **Use proper shebang**: `#!/bin/bash`

### Bash Template:

```bash
#!/bin/bash

set -e

case "$1" in
    description)
        cat <<EOF
{
  "name": "[task_in_snake_case]",
  "description": "{{.task}}",
  "input_schema": {
    "type": "object",
    "properties": {
      "input": {
        "type": "string",
        "description": "Input data for the task"
      }
    },
    "required": ["input"]
  }
}
EOF
        ;;
    run)
        # Read JSON input
        input_json=$(cat)

        # Parse input using jq
        if ! command -v jq &> /dev/null; then
            echo '{"error": "jq is required but not installed"}'
            exit 1
        fi

        input_value=$(echo "$input_json" | jq -r '.input // empty')
        if [ -z "$input_value" ]; then
            echo '{"error": "Missing required parameter: input"}'
            exit 1
        fi

        # Implement your tool logic here based on the task: {{.task}}
        # Example implementation:
        result="Processed: $input_value"

        # Output JSON result
        cat <<EOF
{
  "status": "success",
  "result": "$result"
}
EOF
        ;;
    *)
        echo "Usage: $0 {description|run}" >&2
        exit 1
        ;;
esac
```

## Task-Specific Implementation

For the task "{{.task}}", consider:

1. **What inputs do you need?** Design your schema around the specific requirements
2. **What processing is required?** Choose Python for complex logic or Bash for system operations
3. **What should the output look like?** Structure your results for maximum usefulness
4. **What error cases might occur?** Handle them gracefully with clear messages

## Best Practices

- **Single Responsibility**: Each tool should do one thing well
- **Clear Interface**: Well-documented input schema and output format
- **Robust Error Handling**: Graceful failure with helpful error messages
- **Performance**: Use Python for complex logic, Bash for simple system operations
- **Security**: Validate inputs and sanitize any system interactions
- **Maintainability**: Write clean, well-commented code
- **No Documentation Files**: Do not create README.md or documentation files - the tool's JSON schema and description provide sufficient documentation

## Testing Your Tool

```bash
# Test description
{{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/[task_in_snake_case] description

# Test execution
echo '{"input": "test data"}' | {{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/[task_in_snake_case] run

# Test error handling
echo '{}' | {{if eq .global "true"}}{{.custom_tools_global_dir}}{{else}}{{.custom_tools_local_dir}}{{end}}/[task_in_snake_case] run
```

Now implement your custom tool for the specific requirements of: **{{.task}}**
