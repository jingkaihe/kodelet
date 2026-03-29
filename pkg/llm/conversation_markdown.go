package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const (
	DefaultMaxToolResultCharacters = 2000
	DefaultMaxToolResultBytes      = 100 * 1024
	toolResultTruncationMarker     = "\n[ ... omitted remaining lines to make summarizing use less tokens ... ]"
)

var truncatableToolResultFields = []string{"text", "diff", "output"}

// ConversationMarkdownOptions controls markdown rendering for stored conversations.
type ConversationMarkdownOptions struct {
	TruncateToolResults bool
	MaxToolResultChars  int
	MaxToolResultBytes  int
}

// RenderConversationMarkdown converts a stored conversation into markdown using the
// same formatting logic as `kodelet conversation show --format markdown`.
func RenderConversationMarkdown(
	provider string,
	rawMessages []byte,
	metadata map[string]any,
	toolResults map[string]tooltypes.StructuredToolResult,
) (string, error) {
	return RenderConversationMarkdownWithOptions(provider, rawMessages, metadata, toolResults, ConversationMarkdownOptions{})
}

// RenderConversationMarkdownWithOptions converts a stored conversation into markdown
// with optional output-shaping controls.
func RenderConversationMarkdownWithOptions(
	provider string,
	rawMessages []byte,
	metadata map[string]any,
	toolResults map[string]tooltypes.StructuredToolResult,
	opts ConversationMarkdownOptions,
) (string, error) {
	messages, err := ExtractConversationEntries(provider, rawMessages, metadata, toolResults)
	if err != nil {
		return "", err
	}

	return renderConversationEntriesMarkdown(messages, toolResults, opts), nil
}

func renderConversationEntriesMarkdown(
	messages []conversations.StreamableMessage,
	toolResults map[string]tooltypes.StructuredToolResult,
	opts ConversationMarkdownOptions,
) string {
	var output strings.Builder
	output.WriteString("## Messages\n\n")

	opts = normalizeConversationMarkdownOptions(opts)
	registry := renderers.NewRendererRegistry()
	consumedResults := make(map[int]struct{})

	for i, msg := range messages {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}

		if i > 0 {
			output.WriteString("\n")
		}

		if msg.Kind == "tool-use" {
			resultIndex, resultMsg, hasResult := findMatchingToolResult(messages, i, consumedResults)
			fmt.Fprintf(&output, "### %s\n\n", markdownRoleHeading(msg.Role, "tool-invocation"))
			output.WriteString(renderToolInvocationMarkdown(msg, resultMsg, hasResult, toolResults, registry, opts))
			if hasResult {
				consumedResults[resultIndex] = struct{}{}
			}
			continue
		}

		heading := markdownRoleHeading(msg.Role, msg.Kind)
		fmt.Fprintf(&output, "### %s\n\n", heading)

		switch msg.Kind {
		case "text":
			output.WriteString(renderTextBlockMarkdown(msg))
		case "thinking":
			output.WriteString("<details>\n<summary>Thinking</summary>\n\n")
			output.WriteString(markdownCodeFence("text", msg.Content))
			output.WriteString("\n\n</details>")
		case "tool-result":
			output.WriteString(renderToolResultMarkdown(msg, toolResults, registry, opts))
		default:
			output.WriteString(renderTextBlockMarkdown(msg))
		}
	}

	if len(messages) == 0 {
		output.WriteString("_No messages._\n")
	}

	return strings.TrimRight(output.String(), "\n") + "\n"
}

func markdownRoleHeading(role string, kind string) string {
	base := "Message"
	switch role {
	case "user":
		base = "User"
	case "assistant":
		base = "Assistant"
	case "system":
		base = "System"
	default:
		if role != "" {
			base = strings.ToUpper(role[:1]) + role[1:]
		}
	}

	switch kind {
	case "thinking":
		return base + " · Thinking"
	case "tool-invocation":
		return base + " · Tool"
	case "tool-use":
		return base + " · Tool Call"
	case "tool-result":
		return base + " · Tool Result"
	default:
		return base
	}
}

func renderTextBlockMarkdown(msg conversations.StreamableMessage) string {
	if rendered := renderOpenAIResponsesRawItemMarkdown(msg.RawItem); strings.TrimSpace(rendered) != "" {
		return rendered
	}

	trimmed := strings.TrimSpace(msg.Content)
	if trimmed == "" {
		return "_Empty message._"
	}
	return msg.Content
}

func renderOpenAIResponsesRawItemMarkdown(rawItem json.RawMessage) string {
	if len(rawItem) == 0 {
		return ""
	}

	var rawMessage struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(rawItem, &rawMessage); err != nil || len(rawMessage.Content) == 0 {
		return ""
	}

	var textContent string
	if err := json.Unmarshal(rawMessage.Content, &textContent); err == nil {
		return textContent
	}

	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL string `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(rawMessage.Content, &parts); err != nil {
		return ""
	}

	renderedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			if strings.TrimSpace(part.Text) != "" {
				renderedParts = append(renderedParts, part.Text)
			}
		case "input_image":
			if imageMarkdown := renderInputImageMarkdown(part.ImageURL); imageMarkdown != "" {
				renderedParts = append(renderedParts, imageMarkdown)
			}
		}
	}

	return strings.Join(renderedParts, "\n\n")
}

func renderInputImageMarkdown(imageURL string) string {
	if imageURL == "" {
		return ""
	}

	if strings.HasPrefix(imageURL, "data:") {
		mediaType := mediaTypeFromDataURL(imageURL)
		if mediaType == "" {
			return "_Inline image input._"
		}
		return fmt.Sprintf("_Inline image input (%s)._", mediaType)
	}

	return fmt.Sprintf("Image input: <%s>", imageURL)
}

func mediaTypeFromDataURL(dataURL string) string {
	if !strings.HasPrefix(dataURL, "data:") {
		return ""
	}

	metadata, _, found := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
	if !found {
		return ""
	}

	mediaType, _, _ := strings.Cut(metadata, ";")
	return mediaType
}

func renderToolInvocationMarkdown(
	toolUse conversations.StreamableMessage,
	toolResult conversations.StreamableMessage,
	hasResult bool,
	toolResults map[string]tooltypes.StructuredToolResult,
	registry *renderers.RendererRegistry,
	opts ConversationMarkdownOptions,
) string {
	var output strings.Builder
	toolName := toolUse.ToolName
	if toolName == "" && hasResult {
		toolName = inferToolNameFromResult(toolResult, toolResults)
	}

	if toolName != "" {
		fmt.Fprintf(&output, "- **Tool:** %s\n", inlineMarkdownCode(toolName))
	}
	if toolUse.ToolCallID != "" {
		fmt.Fprintf(&output, "- **Call ID:** %s\n", inlineMarkdownCode(toolUse.ToolCallID))
	}

	renderedInput := registry.RenderToolUseMarkdown(toolUse.ToolName, toolUse.Input)
	if strings.TrimSpace(renderedInput) != "" {
		output.WriteString("\n")
		output.WriteString(renderedInput)
	}

	if hasResult {
		resultBody := renderMergedToolResultMarkdown(toolResult, toolResults, registry, opts)
		if strings.TrimSpace(resultBody) != "" {
			output.WriteString("\n\n**Result**\n\n")
			output.WriteString(resultBody)
		}
	}

	return strings.TrimSpace(output.String())
}

func renderMergedToolResultMarkdown(
	toolResult conversations.StreamableMessage,
	toolResults map[string]tooltypes.StructuredToolResult,
	registry *renderers.RendererRegistry,
	opts ConversationMarkdownOptions,
) string {
	structuredResult, ok := lookupStructuredToolResult(toolResult, toolResults)
	if !ok {
		return renderRawToolPayloadMarkdown(toolResult.Content, opts)
	}

	structuredResult, hardCappedPayload, hardCapped := prepareStructuredToolResultForMarkdown(structuredResult, opts)
	if hardCapped {
		return renderToolPayloadValueMarkdown(hardCappedPayload)
	}

	return registry.RenderMergedMarkdown(structuredResult)
}

func findMatchingToolResult(
	messages []conversations.StreamableMessage,
	toolUseIndex int,
	consumedResults map[int]struct{},
) (int, conversations.StreamableMessage, bool) {
	toolUse := messages[toolUseIndex]
	for i := toolUseIndex + 1; i < len(messages); i++ {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}
		candidate := messages[i]
		if candidate.Kind != "tool-result" {
			continue
		}
		if toolUse.ToolCallID != "" && candidate.ToolCallID == toolUse.ToolCallID {
			return i, candidate, true
		}
		if toolUse.ToolCallID == "" && toolUse.ToolName != "" && candidate.ToolName == toolUse.ToolName {
			return i, candidate, true
		}
	}

	return -1, conversations.StreamableMessage{}, false
}

func renderToolResultMarkdown(
	msg conversations.StreamableMessage,
	toolResults map[string]tooltypes.StructuredToolResult,
	registry *renderers.RendererRegistry,
	opts ConversationMarkdownOptions,
) string {
	var output strings.Builder
	toolName := msg.ToolName
	if toolName == "" {
		toolName = inferToolNameFromResult(msg, toolResults)
	}
	if toolName != "" {
		fmt.Fprintf(&output, "- **Tool:** %s\n", inlineMarkdownCode(toolName))
	}
	if msg.ToolCallID != "" {
		fmt.Fprintf(&output, "- **Call ID:** %s\n", inlineMarkdownCode(msg.ToolCallID))
	}

	if structuredResult, ok := lookupStructuredToolResult(msg, toolResults); ok {
		structuredResult, hardCappedPayload, hardCapped := prepareStructuredToolResultForMarkdown(structuredResult, opts)
		output.WriteString("\n")
		if hardCapped {
			output.WriteString(renderToolPayloadValueMarkdown(hardCappedPayload))
			return strings.TrimSpace(output.String())
		}
		output.WriteString(registry.RenderMarkdown(structuredResult))
		return strings.TrimSpace(output.String())
	}

	if rawPayload := renderRawToolPayloadMarkdown(msg.Content, opts); rawPayload != "" {
		output.WriteString("\n")
		output.WriteString(rawPayload)
	}

	return strings.TrimSpace(output.String())
}

func normalizeConversationMarkdownOptions(opts ConversationMarkdownOptions) ConversationMarkdownOptions {
	if opts.MaxToolResultChars <= 0 {
		opts.MaxToolResultChars = DefaultMaxToolResultCharacters
	}
	if opts.MaxToolResultBytes <= 0 {
		opts.MaxToolResultBytes = DefaultMaxToolResultBytes
	}
	return opts
}

func prepareStructuredToolResultForMarkdown(
	result tooltypes.StructuredToolResult,
	opts ConversationMarkdownOptions,
) (tooltypes.StructuredToolResult, any, bool) {
	rawResult, err := structuredToolResultToMap(result)
	if err != nil {
		return result, nil, false
	}

	if errorText, ok := rawResult["error"].(string); ok {
		rawResult["error"] = truncateToolPayloadString(errorText, opts.MaxToolResultChars)
	}

	if metadata, ok := rawResult["metadata"]; ok {
		processedMetadata, hardCapped := prepareToolPayloadForMarkdown(metadata, opts)
		if hardCapped {
			return tooltypes.StructuredToolResult{}, processedMetadata, true
		}
		rawResult["metadata"] = processedMetadata
	}

	truncatedResult, err := structuredToolResultFromMap(rawResult)
	if err != nil {
		return result, nil, false
	}

	return truncatedResult, nil, false
}

func structuredToolResultToMap(result tooltypes.StructuredToolResult) (map[string]any, error) {
	data, err := result.MarshalJSON()
	if err != nil {
		return nil, err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	return raw, nil
}

func structuredToolResultFromMap(raw map[string]any) (tooltypes.StructuredToolResult, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return tooltypes.StructuredToolResult{}, err
	}

	var result tooltypes.StructuredToolResult
	if err := result.UnmarshalJSON(data); err != nil {
		return tooltypes.StructuredToolResult{}, err
	}

	return result, nil
}

func renderRawToolPayloadMarkdown(content string, opts ConversationMarkdownOptions) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}

	var payload any
	if json.Unmarshal([]byte(trimmed), &payload) == nil {
		processedPayload, _ := prepareToolPayloadForMarkdown(payload, opts)
		return renderToolPayloadValueMarkdown(processedPayload)
	}

	processedPayload, _ := prepareToolPayloadForMarkdown(trimmed, opts)
	if processedText, ok := processedPayload.(string); ok {
		return markdownCodeFence("text", processedText)
	}
	return renderToolPayloadValueMarkdown(processedPayload)
}

func renderToolPayloadValueMarkdown(value any) string {
	if text, ok := value.(string); ok {
		return markdownCodeFence("text", text)
	}

	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return markdownCodeFence("text", fmt.Sprintf("%v", value))
	}

	return markdownCodeFence("json", string(encoded))
}

func prepareToolPayloadForMarkdown(value any, opts ConversationMarkdownOptions) (any, bool) {
	processed := filterTopLevelToolPayloadImages(value)
	if opts.TruncateToolResults {
		processed = truncateToolPayloadValue(processed, opts.MaxToolResultChars)
	}

	byteLen := toolPayloadByteLength(processed)
	if byteLen <= opts.MaxToolResultBytes {
		return processed, false
	}

	return toolPayloadByteLimitPlaceholder(processed, byteLen, opts.MaxToolResultBytes), true
}

func filterTopLevelToolPayloadImages(value any) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}

	filtered := make([]any, 0, len(items))
	for _, item := range items {
		if isToolPayloadImage(item) {
			continue
		}
		filtered = append(filtered, item)
	}

	return filtered
}

func isToolPayloadImage(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}

	itemType, ok := object["type"].(string)
	return ok && itemType == "image"
}

func truncateToolPayloadValue(value any, maxChars int) any {
	switch v := value.(type) {
	case string:
		return truncateToolPayloadString(v, maxChars)
	case []any:
		return truncateToolPayloadArray(v, maxChars)
	case map[string]any:
		return truncateToolPayloadObject(v, maxChars)
	default:
		return value
	}
}

func truncateToolPayloadArray(items []any, maxChars int) []any {
	totalLength := 0
	truncated := make([]any, 0, len(items))

	for _, item := range items {
		candidate := item
		switch v := item.(type) {
		case string:
			candidate = truncateToolPayloadString(v, maxChars)
		case map[string]any:
			candidate = truncateToolPayloadObject(v, maxChars)
		}

		candidateLength := toolPayloadSerializedLength(candidate)
		originalLength := toolPayloadSerializedLength(item)
		if totalLength+candidateLength > maxChars {
			if totalLength == 0 && originalLength > candidateLength {
				truncated = append(truncated, candidate)
			} else {
				truncated = append(truncated, toolResultTruncationMarker)
			}
			break
		}

		totalLength += candidateLength
		truncated = append(truncated, candidate)
	}

	return truncated
}

func truncateToolPayloadObject(object map[string]any, maxChars int) map[string]any {
	cloned := make(map[string]any, len(object))
	for key, value := range object {
		cloned[key] = value
	}

	for _, field := range truncatableToolResultFields {
		if fieldValue, ok := cloned[field].(string); ok {
			cloned[field] = truncateToolPayloadString(fieldValue, maxChars)
		}
	}

	return cloned
}

func truncateToolPayloadString(value string, maxChars int) string {
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[:maxChars] + toolResultTruncationMarker
}

func toolPayloadSerializedLength(value any) int {
	switch v := value.(type) {
	case string:
		return len(v)
	default:
		return len(toolPayloadJSON(value))
	}
}

func toolPayloadByteLength(value any) int {
	return len(toolPayloadJSON(value))
}

func toolPayloadJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(fmt.Sprintf("%v", value))
	}
	return data
}

func toolPayloadByteLimitPlaceholder(value any, actualBytes int, maxBytes int) any {
	message := fmt.Sprintf(
		"[Tool result truncated: %dKB exceeds limit of %dKB. Please refine the query.]",
		int(math.Round(float64(actualBytes)/1024)),
		int(math.Round(float64(maxBytes)/1024)),
	)

	if _, ok := value.([]any); ok {
		return []any{message}
	}

	return message
}

func lookupStructuredToolResult(msg conversations.StreamableMessage, toolResults map[string]tooltypes.StructuredToolResult) (tooltypes.StructuredToolResult, bool) {
	if msg.ToolCallID != "" {
		if result, ok := toolResults[msg.ToolCallID]; ok {
			return result, true
		}
	}
	if msg.ToolName != "" {
		if result, ok := toolResults[msg.ToolName]; ok {
			return result, true
		}
	}
	return tooltypes.StructuredToolResult{}, false
}

func inferToolNameFromResult(msg conversations.StreamableMessage, toolResults map[string]tooltypes.StructuredToolResult) string {
	if result, ok := lookupStructuredToolResult(msg, toolResults); ok {
		return result.ToolName
	}
	return msg.ToolName
}

func markdownCodeFence(language string, content string) string {
	return renderers.FencedCodeBlock(language, content)
}

func inlineMarkdownCode(value string) string {
	if strings.Contains(value, "`") {
		return fmt.Sprintf("``%s``", value)
	}
	return fmt.Sprintf("`%s`", value)
}

func trimJSONForMarkdown(value string) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(value), "", "  "); err == nil {
		return pretty.String()
	}
	return value
}
