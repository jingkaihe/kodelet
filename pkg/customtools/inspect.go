// Package customtools contains shared helpers for inspecting custom tool
// executables across runtime discovery and plugin metadata listing.
package customtools

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
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

// Config represents the optional JSON structure returned by a custom tool's
// config command.
type Config struct {
	Timeout string `json:"timeout"`
}

// RuntimeConfig contains validated runtime defaults exposed by a custom tool.
type RuntimeConfig struct {
	Timeout time.Duration
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

// InspectConfig runs a custom tool's optional config command and validates the
// runtime defaults it exposes. A command execution error means the optional
// command is unavailable and is not treated as a failure.
func InspectConfig(ctx context.Context, execPath string, timeout time.Duration) (*RuntimeConfig, error) {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, execPath, "config")
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return &RuntimeConfig{}, nil
	}

	if strings.TrimSpace(stdout.String()) == "" {
		return &RuntimeConfig{}, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		return &RuntimeConfig{}, nil
	}

	var config Config
	if err := json.Unmarshal(stdout.Bytes(), &config); err != nil {
		return nil, errors.Wrap(err, "failed to parse tool config")
	}

	runtimeConfig := &RuntimeConfig{}
	if config.Timeout != "" {
		timeout, err := time.ParseDuration(config.Timeout)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse tool config timeout")
		}
		runtimeConfig.Timeout = timeout
	}

	return runtimeConfig, nil
}
