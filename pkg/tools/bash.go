package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/olekukonko/tablewriter"
	"go.opentelemetry.io/otel/attribute"
)

var (
	BannedCommands = []string{
		"vim",
		"view",
		"less",
		"more",
		"cd",
	}
)

type BashTool struct{}

type BashInput struct {
	Description string `json:"description" jsonschema:"description=A description of the command to run"`
	Command     string `json:"command" jsonschema:"description=The bash command to run"`
	Timeout     int    `json:"timeout" jsonschema:"description=The timeout for the command in seconds,default=10"`
}

func (b *BashTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[BashInput]()
}

func (b *BashTool) Name() string {
	return "bash"
}

func (b *BashTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &BashInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("command", input.Command),
		attribute.String("description", input.Description),
		attribute.Int("timeout", input.Timeout),
	}, nil
}

func (b *BashTool) ValidateInput(state tooltypes.State, parameters string) error {
	input := &BashInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	if input.Command == "" {
		return errors.New("command is required")
	}

	if input.Description == "" {
		return errors.New("description is required")
	}

	if input.Timeout < 10 || input.Timeout > 120 {
		return errors.New("timeout must be between 10 and 120 seconds")
	}

	validateCommand := func(command string) error {
		command = strings.TrimSpace(command)
		if command == "" {
			return nil
		}

		splitted := strings.Split(command, " ")
		if len(splitted) == 0 {
			return errors.New("command must contain at least one word")
		}

		firstWord := splitted[0]
		if slices.Contains(BannedCommands, firstWord) {
			return errors.New("command is banned: " + firstWord)
		}

		return nil
	}

	// Split by all operators and validate each command
	operators := []string{"&&", "||", ";"}
	commands := []string{input.Command}

	for _, op := range operators {
		var newCommands []string
		for _, cmd := range commands {
			newCommands = append(newCommands, strings.Split(cmd, op)...)
		}
		commands = newCommands
	}

	for _, command := range commands {
		if err := validateCommand(command); err != nil {
			return err
		}
	}

	return nil
}

func (b *BashTool) Description() string {
	return `Executes a given bash command in a persistent shell session with timeout.

Before executing the command, please follow these steps:

# Important
* The command argument is required.
* You must specify a timeout from 10 to 120 seconds.
* You **MUST** use batch tool to wrap multiple independent commands together.
* Please provide a clear and concise description of what this command does in 5-10 words.
* If the output exceeds 30000 characters, output will be truncated before being returned to you.
* You **MUST NOT** run commands that require user interaction.
* When issuing multiple commands, use the ';' or '&&' operator to separate them. Command MUST NOT be multiline.
* Try to maintain your current working directory throughout the session by using absolute paths and avoid using cd directly. If you need to use cd please wrap it in parentheses.
* grep_tool and glob_tool are prefered over running grep, egrep and find using the bash tool.
* DO NOT use heredoc. For any command that requires heredoc, use the "file_write" tool instead.

# Examples
<good-example>
pytest /foo/bar/tests
</good-example>

<bad-example>
cd /foo/bar && pytest tests
<reasoning>
Using cd directly changes the current working directory.
</reasoning>
</bad-example>

<good-example>
(cd /foo/bar && pytest tests)
<reasoning>
cd command is wrapped in parentheses thus avoid changing the current working directory.
</reasoning>
</good-example>

<good-example>
apt-get install -y python3-pytest
</good-example>

<bad-example>
apt-get install python3-pytest
<reasoning>
The command requires user interaction.
</reasoning>
</bad-example>

<bad-example>
tail -f /var/log/nginx/access.log
<reasoning>
The command is running in interactive mode.
</reasoning>
</bad-example>

<bad-example>
vim /foo/bar/tests.py
<reasoning>
The command is running in interactive mode.
</reasoning>
</bad-example>

<good-example>
echo a; echo b
</good-example>

<bad-example>
echo a
echo b
<reasoning>
The command is multiline.
</reasoning>
</bad-example>

<bad-example>
cat <<EOF > /foo/bar/tests.py
import pytest

def test_foo():
    assert 1 == 1
EOF
<reasoning>
The command is using heredoc.
</reasoning>
</bad-example>
`
}

type BashToolResult struct {
	CombinedOutput string `json:"combined_output"`
	Error          string `json:"error"`
}

func (r *BashToolResult) GetResult() string {
	return r.CombinedOutput
}

func (r *BashToolResult) GetError() string {
	return r.Error
}

func (r *BashToolResult) IsError() bool {
	return r.Error != ""
}

func (r *BashToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.CombinedOutput, r.GetError())
}

func (r *BashToolResult) UserFacing() string {
	var buf bytes.Buffer

	table := tablewriter.NewWriter(&buf)

	if r.IsError() {
		table.Append([]string{"Error", r.GetError()})
	}

	output := r.CombinedOutput
	if output == "" {
		output = "(no output)"
	}
	table.Append([]string{"Output", output})

	// Render the table
	table.Render()

	return buf.String()
}

func (b *BashTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResultInterface {
	input := &BashInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &BashToolResult{
			Error: err.Error(),
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &BashToolResult{
				Error: "Command timed out after " + strconv.Itoa(input.Timeout) + " seconds",
			}
		}
		if status, ok := err.(*exec.ExitError); ok {
			return &BashToolResult{
				CombinedOutput: string(output),
				Error:          fmt.Sprintf("Command exited with status %d", status.ExitCode()),
			}
		}
		return &BashToolResult{
			Error: err.Error(),
		}
	}

	return &BashToolResult{
		CombinedOutput: string(output),
	}
}
