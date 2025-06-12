package gha

import (
	"bytes"
	"embed"
	"text/template"

	"github.com/pkg/errors"
)

// Template files
//
//go:embed templates/*
var TemplateFS embed.FS

const (
	// Template file paths
	BackgroundAgentWorkflowTemplate = "templates/background_agent_workflow.yaml.tmpl"
)

// WorkflowTemplateData holds the data for workflow template rendering
type WorkflowTemplateData struct {
	AuthGatewayEndpoint string
}

// RenderBackgroundAgentWorkflow renders the background agent workflow template
func RenderBackgroundAgentWorkflow(data WorkflowTemplateData) (string, error) {
	tmplContent, err := TemplateFS.ReadFile(BackgroundAgentWorkflowTemplate)
	if err != nil {
		return "", errors.Wrap(err, "failed to read template file")
	}

	tmpl, err := template.New("workflow").Parse(string(tmplContent))
	if err != nil {
		return "", errors.Wrap(err, "failed to parse template")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", errors.Wrap(err, "failed to execute template")
	}

	return buf.String(), nil
}