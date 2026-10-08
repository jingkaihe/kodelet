package controlplane

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/vision"
	"github.com/pkg/errors"
)

var imageShortCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

func (s *Server) imageViewURL(shortCode string) string {
	if !imageShortCodePattern.MatchString(shortCode) {
		return ""
	}
	base := ""
	if s.config != nil {
		base = s.config.PublicBaseURL
	}
	return strings.TrimRight(base, "/") + "/i/" + shortCode
}

func (s *Server) decorateImageAttachments(result tooltypes.StructuredToolResult) tooltypes.StructuredToolResult {
	result.Attachments = append([]tooltypes.ToolAttachment(nil), result.Attachments...)
	for i := range result.Attachments {
		attachment := &result.Attachments[i]
		attachment.ViewURL = ""
		if attachment.Type == "image" && attachment.ArtifactID != "" && attachment.Error == "" {
			attachment.ViewURL = s.imageViewURL(attachment.ShortCode)
		}
	}
	return result
}

func (s *Server) handleImageLink(w http.ResponseWriter, r *http.Request) {
	if s.artifacts == nil {
		http.NotFound(w, r)
		return
	}
	attachment, path, err := s.artifacts.GetByShortCode(r.Context(), mux.Vars(r)["shortCode"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Like conversations, images are shared among authenticated users with the
	// user role. A short code never bypasses authentication or grants API access.
	s.serveImage(w, r, attachment, path)
}

func (s *Server) handleConversationArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifacts == nil {
		http.NotFound(w, r)
		return
	}
	vars := mux.Vars(r)
	attachment, path, err := s.artifacts.Get(r.Context(), vars["id"], vars["artifactId"])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveImage(w, r, attachment, path)
}

func (*Server) serveImage(w http.ResponseWriter, r *http.Request, attachment tooltypes.ToolAttachment, path string) {
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", attachment.MimeType)
	w.Header().Set(
		"Content-Disposition",
		mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Filename}),
	)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.ServeContent(w, r, attachment.Filename, info.ModTime(), file)
}

// artifactController validates runner references and materializes images beside
// the model client, after the runner has applied its ordinary tool lifecycle.
type artifactController struct {
	agentenv.RemoteController
	server         *Server
	conversationID string
	config         llmtypes.Config
}

func (c artifactController) ExecuteTool(
	ctx context.Context,
	params runnerpayload.ToolExecuteParams,
	updates func(runnerpayload.ToolUpdateParams),
) (runnerpayload.ToolExecuteResult, error) {
	result, err := c.RemoteController.ExecuteTool(ctx, params, updates)
	if err != nil {
		return result, err
	}
	if len(result.Result.Structured.Attachments) == 0 && !hasArtifactContent(result.Result.ContentParts) {
		return result, nil
	}
	// Preserve extension mutations as authoritative text, just as the base tool
	// executor does, before adding host-owned descriptors. Mutations must not
	// accidentally reintroduce image content the hook replaced.
	if result.Modified {
		text := renderers.NewRendererRegistry().Render(result.Result.Structured)
		result.Result.AssistantFacing = tooltypes.StringifyToolResult(text, result.Result.Structured.Error)
		result.Result.DisplayOutput = text
		result.Result.Error = result.Result.Structured.Error
		result.Result.ContentParts = nil
		var metadata tooltypes.CodeExecutionMetadata
		if result.Result.Structured.ToolName == "code_execute" && tooltypes.ExtractMetadata(result.Result.Structured.Metadata, &metadata) {
			// The runner prunes code selections against the original host-owned
			// emissions after hooks. Reconstruct only that effective snapshot.
			code := tools.CodeExecuteResult{Metadata: metadata, Error: result.Result.Error, Attachments: result.Result.Structured.Attachments}
			result.Result.AssistantFacing = code.AssistantFacing()
			result.Result.ContentParts = code.ContentParts()
		}
		result.Modified = false
	}
	c.normalizeAttachments(ctx, &result.Result)
	if err := c.materializeImages(ctx, &result.Result); err != nil {
		result.Result.Error = err.Error()
		result.Result.Structured.Success = false
		result.Result.Structured.Error = err.Error()
		result.Result.AssistantFacing = tooltypes.StringifyToolResult("", err.Error())
		result.Result.ContentParts = nil
	}
	result.Result.Structured = c.server.decorateImageAttachments(result.Result.Structured)
	return result, nil
}

func hasArtifactContent(parts []tooltypes.ToolResultContentPart) bool {
	for _, part := range parts {
		if part.ArtifactID != "" {
			return true
		}
	}
	return false
}

func (c artifactController) normalizeAttachments(ctx context.Context, result *runnerpayload.ToolResult) {
	viewed := make(map[string]bool)
	for _, part := range result.ContentParts {
		if part.Type == tooltypes.ToolResultContentPartTypeImage {
			viewed[part.ArtifactID] = true
		}
	}
	attachments := result.Structured.Attachments
	if len(attachments) > runnerpayload.MaxToolAttachments+1 {
		attachments = attachments[:runnerpayload.MaxToolAttachments+1]
	}
	for i, attachment := range attachments {
		var err error
		if attachment.Error == "" && c.server.artifacts != nil {
			var stored tooltypes.ToolAttachment
			stored, _, err = c.server.artifacts.Get(ctx, c.conversationID, attachment.ArtifactID)
			if err == nil && attachment.Type == "image" {
				stored.Alt = attachment.Alt
				attachments[i] = stored
				if result.Structured.ToolName == "view_image" {
					var metadata tooltypes.ViewImageMetadata
					if tooltypes.ExtractMetadata(result.Structured.Metadata, &metadata) {
						metadata.ArtifactID = stored.ArtifactID
						result.Structured.Metadata = &metadata
					}
					if len(result.ContentParts) > 0 {
						// Multimodal providers use content parts instead of AssistantFacing.
						// Keep the handle beside the pixels, out of UI display output.
						text := "Artifact ID: " + stored.ArtifactID
						result.AssistantFacing = tooltypes.StringifyToolResult(text, "")
						result.ContentParts = append([]tooltypes.ToolResultContentPart{{
							Type: tooltypes.ToolResultContentPartTypeText,
							Text: text,
						}}, result.ContentParts...)
						continue
					}
				}
				hint := fmt.Sprintf(
					"\n\nImage artifact: %s (%dx%d, %s).",
					stored.ArtifactID,
					stored.Width,
					stored.Height,
					stored.MimeType,
				)
				if !viewed[stored.ArtifactID] {
					hint += " Use view_image with artifactId to inspect it."
				}
				if imageURL := c.server.imageViewURL(stored.ShortCode); imageURL != "" {
					hint += "\nImage URL: " + imageURL + "\nUse this URL when linking the image in your reply, not a runner-local path."
				}
				result.AssistantFacing += hint
				if result.Structured.ToolName == "code_execute" && len(result.ContentParts) > 0 {
					result.ContentParts = append(result.ContentParts, tooltypes.ToolResultContentPart{Type: tooltypes.ToolResultContentPartTypeText, Text: hint})
				}
				continue
			}
		}
		attachment.Path, attachment.ArtifactID, attachment.ShortCode, attachment.ViewURL = "", "", "", ""
		if attachment.Error == "" {
			attachment.Error = "Image attachment is not available in this conversation"
		}
		attachments[i] = attachment
		result.AssistantFacing += "\n\n" + attachment.Error
	}
	result.Structured.Attachments = attachments
}

func (c artifactController) materializeImages(ctx context.Context, result *runnerpayload.ToolResult) error {
	// A code execution result reports child side effects that already
	// happened. Losing one selected image must not hide them, or the model is
	// likely to rerun the script; replace only that image with a notice.
	partial := result.Structured.ToolName == "code_execute"
	for i, part := range result.ContentParts {
		if part.ArtifactID == "" {
			continue
		}
		materialized, err := c.materializeImage(ctx, part)
		if err != nil {
			if !partial {
				return err
			}
			notice := "Image " + part.ArtifactID + " could not be sent to the model: " + err.Error()
			result.ContentParts[i] = tooltypes.ToolResultContentPart{
				Type: tooltypes.ToolResultContentPartTypeText,
				Text: notice,
			}
			result.AssistantFacing += "\n\n" + notice
			continue
		}
		result.ContentParts[i] = materialized
	}
	return nil
}

func (c artifactController) materializeImage(
	ctx context.Context,
	part tooltypes.ToolResultContentPart,
) (tooltypes.ToolResultContentPart, error) {
	if c.server.artifacts == nil || part.Type != tooltypes.ToolResultContentPartTypeImage {
		return part, errors.New("image artifact storage is unavailable")
	}
	_, path, err := c.server.artifacts.Get(ctx, c.conversationID, part.ArtifactID)
	if err != nil {
		return part, errors.New("image artifact is not available in this conversation")
	}
	file, err := os.Open(path)
	if err != nil {
		return part, errors.Wrap(err, "failed to open image artifact")
	}
	data, err := io.ReadAll(io.LimitReader(file, runnerpayload.MaxArtifactBytes+1))
	_ = file.Close()
	if err != nil {
		return part, errors.Wrap(err, "failed to read image artifact")
	}
	image, err := vision.MakeViewImageResultBytes(data, part.ArtifactID, part.Detail, c.config.Model, c.config.Provider)
	if err != nil {
		return part, err
	}
	part.ImageURL, part.MimeType, part.Detail, part.ArtifactID = image.ImageURL, image.MimeType, image.Detail, ""
	return part, nil
}
