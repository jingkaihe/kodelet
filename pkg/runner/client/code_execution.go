package client

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/codemode"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/vision"
	"github.com/pkg/errors"
)

func (s *Service) codeToolAllowed(run *activeRun, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !run.config.EnvironmentOptions().ToolAllowed(name) {
		return false
	}
	if run.codeAllowedTools != nil && !slices.Contains(*run.codeAllowedTools, name) {
		return false
	}
	return codeConfigToolAllowed(run.config, name)
}

func codeConfigToolAllowed(config llmtypes.Config, name string) bool {
	if len(config.AllowedTools) == 0 {
		return true
	}
	for _, allowed := range config.AllowedTools {
		if strings.TrimSpace(allowed) == name {
			return true
		}
	}
	return config.EnableFSSearchTools && (name == "grep_tool" || name == "glob_tool")
}

func (s *Service) codeExecutionContext(ctx context.Context, run *activeRun, params runnerpayload.ToolExecuteParams) (tools.CodeExecutionContext, error) {
	if !run.codeExecution || !run.manifest.Capabilities.CodeExecution || params.CallableTools == nil ||
		params.ManifestDigest == "" || params.ManifestDigest != run.manifest.Digest || !s.codeToolAllowed(run, "code_execute") {
		return tools.CodeExecutionContext{}, errors.New("code execution requires authorized tools and a negotiated pinned runner manifest")
	}
	peer := s.currentPeer()
	if peer == nil {
		return tools.CodeExecutionContext{}, errors.New("code execution requires an active control-plane connection")
	}
	available := make(map[string]runnerpayload.ToolDefinition, len(run.manifest.Tools))
	for _, definition := range run.manifest.Tools {
		// Model-only tools are declared directly and never callable from scripts.
		if definition.Placement == "environment" && !definition.ModelOnly {
			available[definition.Name] = definition
		}
	}
	if _, exists := available["code_execute"]; !exists {
		return tools.CodeExecutionContext{}, errors.New("code execution is not available in the pinned manifest")
	}
	allowed := make(map[string]bool, len(*params.CallableTools))
	definitions := make([]codemode.Definition, 0, len(*params.CallableTools))
	for _, name := range *params.CallableTools {
		definition, exists := available[name]
		if !exists || name == "code_execute" {
			return tools.CodeExecutionContext{}, errors.New("code execution callable set exceeds the pinned manifest")
		}
		if allowed[name] || !s.codeToolAllowed(run, name) {
			continue
		}
		allowed[name] = true
		definitions = append(definitions, codemode.Definition{
			Name:         name,
			Description:  definition.Description,
			Group:        definition.Group,
			Short:        definition.Short,
			InputSchema:  definition.InputSchema,
			OutputSchema: definition.OutputSchema,
		})
	}
	return tools.CodeExecutionContext{
		Definitions: definitions,
		ValidateImage: func(detail string) error {
			if !allowed["view_image"] || !s.codeToolAllowed(run, "view_image") {
				return errors.New("image emission requires view_image permission")
			}
			s.mu.Lock()
			model := run.config.Model
			s.mu.Unlock()
			_, err := vision.NormalizeViewImageDetail(detail, model)
			return err
		},
		Call: func(callCtx context.Context, name, input, callID string, update func(tools.CodeToolReply)) (tools.CodeToolReply, error) {
			childError := func(kind, outcome, message string) (tools.CodeToolReply, error) {
				return tools.CodeToolReply{}, &tools.CodeToolError{
					Kind:    kind,
					Tool:    name,
					CallID:  callID,
					Outcome: outcome,
					Message: message,
				}
			}
			if !allowed[name] || name == "code_execute" || !s.codeToolAllowed(run, name) {
				return childError("blocked", "not_started", "tool is not in the authorized catalog")
			}
			// The runtime supplies a child context, but the parent independently fences its lifetime.
			childCtx, cancel := context.WithCancel(callCtx)
			stop := context.AfterFunc(ctx, cancel)
			defer stop()
			defer cancel()
			if ctx.Err() != nil {
				cancel()
			}
			childRun, operationCtx, finish, err := s.beginRunOperation(childCtx, run.id)
			if err != nil {
				return childError("cancelled", "not_started", err.Error())
			}
			defer finish()
			if childRun != run {
				return childError("cancelled", "not_started", "code execution run changed")
			}
			ownership := runnerpayload.ToolChildParams{
				RunID:            run.id,
				ParentToolCallID: params.ToolCallID,
				ToolCallID:       callID,
				Name:             name,
			}
			if err := peer.Call(operationCtx, protocol.MethodToolChildBegin, ownership, new(struct{})); err != nil {
				return childError("transport", "not_started", "child registration was not acknowledged: "+err.Error())
			}
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(operationCtx), 2*time.Second)
				defer cleanupCancel()
				if err := peer.Call(cleanupCtx, protocol.MethodToolChildEnd, ownership, new(struct{})); err != nil {
					logger.G(operationCtx).WithError(err).Debug("code child ownership cleanup not acknowledged; parent cleanup will revoke authority")
				}
			}()
			if err := operationCtx.Err(); err != nil {
				return childError("cancelled", "not_started", err.Error())
			}
			var childUpdates agentenv.ToolUpdateSink
			if update != nil {
				childUpdates = func(snapshot agentenv.ToolUpdate) {
					// Input is already post-policy and cleared after output redaction.
					// Never consult the raw ToolResult or restore original arguments.
					structured := snapshot.StructuredResult
					structured.Data, structured.Attachments = nil, nil
					update(tools.CodeToolReply{
						Input:  json.RawMessage(snapshot.Input),
						Result: &structured,
					})
				}
			}
			execution, err := s.executeRunTool(operationCtx, run, runnerpayload.ToolExecuteParams{
				RunID:      run.id,
				ToolCallID: callID,
				Name:       name,
				Input:      json.RawMessage(input),
			}, true, childUpdates)
			if err != nil {
				kind := "transport"
				if operationCtx.Err() != nil {
					kind = "cancelled"
				}
				return childError(kind, "unknown", err.Error())
			}
			reply := codeReply(execution)
			if execution.FailureKind != "" || !execution.Result.Structured.Success {
				kind, outcome := execution.FailureKind, execution.FailureOutcome
				if kind == "" {
					kind = "tool_error"
				}
				if outcome == "" {
					outcome = "completed"
				}
				return reply, &tools.CodeToolError{
					Kind:    kind,
					Tool:    name,
					CallID:  callID,
					Outcome: outcome,
					Message: execution.Result.Structured.Error,
					Result:  &reply,
				}
			}
			return reply, nil
		},
	}, nil
}

func codeReply(execution runnerpayload.ToolExecuteResult) tools.CodeToolReply {
	structured := execution.Result.Structured
	reply := tools.CodeToolReply{
		Data:        structured.Data,
		Attachments: append([]tooltypes.ToolAttachment{}, structured.Attachments...),
		Input:       execution.Input,
		Result:      &structured,
	}
	if execution.Modified {
		// Legacy result hooks may redact display output without knowing machine data.
		reply.Data = nil
		// Do not restore arguments that a result hook may have removed from metadata.
		reply.Input = nil
	}
	for i := range reply.Attachments {
		// A failed upload must not expose a runner-local path as a usable artifact.
		reply.Attachments[i].Path = ""
	}
	var bash tooltypes.BashMetadata
	var file tooltypes.FileReadMetadata
	var extension tooltypes.ExtensionToolMetadata
	var web tooltypes.WebFetchMetadata
	var grep tooltypes.GrepMetadata
	var glob tooltypes.GlobMetadata
	switch {
	case tooltypes.ExtractMetadata(structured.Metadata, &bash):
		reply.Text = bash.Output
		reply.Truncated = bash.Truncation != nil && bash.Truncation.Truncated
	case tooltypes.ExtractMetadata(structured.Metadata, &file):
		// The canonical lines already carry file content. Retain effective text
		// only when machine data is unavailable, including hook redaction.
		if reply.Data == nil {
			reply.Text = strings.Join(file.Lines, "\n")
		}
		reply.Truncated = file.Truncated
	case tooltypes.ExtractMetadata(structured.Metadata, &extension):
		reply.Text = extension.Output
		reply.Truncated = extension.Truncated
	case tooltypes.ExtractMetadata(structured.Metadata, &web):
		reply.Text = web.Content
	default:
		if execution.Modified || structured.Metadata != nil {
			reply.Text = renderers.NewRendererRegistry().Render(structured)
		} else {
			reply.Text = execution.Result.DisplayOutput
		}
		if tooltypes.ExtractMetadata(structured.Metadata, &grep) {
			reply.Truncated = grep.Truncated
		} else if tooltypes.ExtractMetadata(structured.Metadata, &glob) {
			reply.Truncated = glob.Truncated
		}
	}
	return reply
}
