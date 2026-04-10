package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/vision"
)

// ViewImageInput defines the input parameters for the view_image tool.
type ViewImageInput struct {
	Path   string `json:"path" jsonschema:"description=Local filesystem path to an image file. Absolute paths are preferred."`
	Detail string `json:"detail,omitempty" jsonschema:"description=Optional detail override. The only supported value is 'original'; omit this field for default resized behavior."`
}

// ViewImageToolResult represents the result of a view_image operation.
type ViewImageToolResult struct {
	base tooltypes.BaseToolResult
	data *vision.Result
}

func (r *ViewImageToolResult) AssistantFacing() string {
	if r.base.Error != "" {
		return r.base.AssistantFacing()
	}
	if r.data == nil {
		return tooltypes.StringifyToolResult("(No output)", "")
	}
	return tooltypes.StringifyToolResult(r.data.Assistant, "")
}

func (r *ViewImageToolResult) IsError() bool { return r.base.IsError() }

func (r *ViewImageToolResult) GetError() string { return r.base.GetError() }

func (r *ViewImageToolResult) GetResult() string {
	if r.data == nil {
		return r.base.GetResult()
	}
	return r.data.Assistant
}

func (r *ViewImageToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "view_image",
		Success:   !r.IsError(),
		Error:     r.GetError(),
		Timestamp: time.Now(),
	}
	if r.data != nil {
		result.Metadata = vision.MetadataFromResult(r.data)
	}
	return result
}

func (r *ViewImageToolResult) ContentParts() []tooltypes.ToolResultContentPart {
	if r.data == nil || r.base.Error != "" {
		return nil
	}
	return []tooltypes.ToolResultContentPart{{
		Type:     tooltypes.ToolResultContentPartTypeImage,
		ImageURL: r.data.ImageURL,
		MimeType: r.data.MimeType,
		Detail:   r.data.Detail,
	}}
}

// ViewImageTool implements the view_image tool for local image inspection.
type ViewImageTool struct {
	model    string
	provider string
}

// NewViewImageTool creates a view_image tool configured for the active model/provider.
func NewViewImageTool(model, provider string) *ViewImageTool {
	return &ViewImageTool{model: model, provider: provider}
}

func (t *ViewImageTool) Name() string {
	return "view_image"
}

func (t *ViewImageTool) GenerateSchema() *jsonschema.Schema {
	schema := GenerateSchema[ViewImageInput]()
	if schema != nil && schema.Properties != nil && !vision.SupportsViewImageOriginalDetail(t.model) {
		schema.Properties.Delete("detail")
	}
	return schema
}

func (t *ViewImageTool) Description() string {
	detailText := "This model does not support the optional `detail` field; omit it."
	if vision.SupportsViewImageOriginalDetail(t.model) {
		detailText = "The optional `detail` field is available for this model and supports only `original`. Use it when high-fidelity image perception or precise localization is needed."
	}
	return "View a local image from the filesystem (only use if given a full filepath by the user, and the image isn't already attached in the conversation context).\n\n" + detailText
}

func (t *ViewImageTool) ValidateInput(state tooltypes.State, parameters string) error {
	input := &ViewImageInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return err
	}
	if strings.TrimSpace(input.Path) == "" {
		return errors.New("path is required")
	}
	if input.Detail != "" {
		var model string
		if state != nil {
			if cfg, ok := state.GetLLMConfig().(llmtypes.Config); ok {
				model = cfg.Model
			}
		}
		if _, err := vision.NormalizeViewImageDetail(input.Detail, model); err != nil {
			return err
		}
	}
	return nil
}

func (t *ViewImageTool) Execute(_ context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &ViewImageInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return &ViewImageToolResult{base: tooltypes.BaseToolResult{Error: err.Error()}}
	}

	resolved := strings.TrimSpace(input.Path)
	if state != nil && resolved != "" && !filepath.IsAbs(strings.TrimPrefix(resolved, "file://")) {
		resolved = filepath.Join(state.WorkingDirectory(), resolved)
	}

	provider := t.provider
	model := t.model
	if state != nil {
		if cfg, ok := state.GetLLMConfig().(llmtypes.Config); ok {
			provider = cfg.Provider
			model = cfg.Model
		}
	}

	result, err := vision.MakeViewImageResult(resolved, input.Detail, model, provider)
	if err != nil {
		return &ViewImageToolResult{base: tooltypes.BaseToolResult{Error: err.Error()}}
	}

	return &ViewImageToolResult{
		base: tooltypes.BaseToolResult{Result: result.Assistant},
		data: result,
	}
}

func (t *ViewImageTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &ViewImageInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return nil, err
	}
	attrs := []attribute.KeyValue{
		attribute.String("path", input.Path),
	}
	if strings.TrimSpace(input.Detail) != "" {
		attrs = append(attrs, attribute.String("detail", strings.TrimSpace(input.Detail)))
	}
	return attrs, nil
}
