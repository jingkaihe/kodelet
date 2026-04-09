// Package customtools contains shared helpers for inspecting custom tool
// executables across runtime discovery and plugin metadata listing.
package customtools

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
)

// Description represents the JSON structure returned by a custom tool's
// description command.
type Description struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Metadata contains the validated metadata exposed by a custom tool.
type Metadata struct {
	Name        string
	Description string
	Schema      *jsonschema.Schema
}

// Inspect runs a custom tool's description command and validates the metadata
// it exposes.
func Inspect(ctx context.Context, execPath string, timeout time.Duration) (*Metadata, error) {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, execPath, "description")
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, errors.Wrapf(err, "failed to run description command: %s", stderr.String())
	}

	var desc Description
	if err := json.Unmarshal(stdout.Bytes(), &desc); err != nil {
		return nil, errors.Wrap(err, "failed to parse tool description")
	}

	if desc.Name == "" {
		return nil, errors.New("tool name is required")
	}
	if desc.Description == "" {
		return nil, errors.New("tool description is required")
	}

	schemaBytes, err := json.Marshal(desc.InputSchema)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal input schema")
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil, errors.Wrap(err, "failed to parse input schema")
	}

	return &Metadata{
		Name:        desc.Name,
		Description: desc.Description,
		Schema:      &schema,
	}, nil
}
