package base

import (
	"encoding/json"
	"sort"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// EstimateContextBreakdown reads the actual provider wire representation. Pass
// only the live inference history (not display archives or structured results),
// and the system prompt and tool definitions after provider-specific filtering.
// Text uses the same rough four-bytes-per-token heuristic as context compaction.
// Media, signatures and encrypted reasoning have no useful character estimate
// and are omitted rather than treating their base64 transport size as tokens.
func EstimateContextBreakdown(system, definitions, messages any, codeMode string) *llmtypes.ContextBreakdown {
	if codeMode == "" {
		codeMode = "off"
	}
	decode := func(value any) (any, bool) {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var result any
		if json.Unmarshal(raw, &result) != nil {
			return nil, false
		}
		return result, true
	}
	systemValue, ok := decode(system)
	if !ok {
		return nil
	}
	toolValue, ok := decode(definitions)
	if !ok {
		return nil
	}
	history, ok := decode(messages)
	if !ok {
		return nil
	}
	c := contextCounter{
		system: contextTextSize(systemValue),
		tools:  make(map[string]*toolContextSize),
		names:  make(map[string]string),
	}
	if tools, ok := toolValue.([]any); ok && len(tools) > 0 {
		c.definitions = contextJSONSize(tools)
	}
	items, _ := history.([]any)
	// Resolve names first so results still work if a provider groups them before
	// their calls. Only model-visible calls populate this map.
	for _, item := range items {
		c.visit(item, true)
	}
	for _, item := range items {
		c.visit(item, false)
	}
	b := &llmtypes.ContextBreakdown{
		SystemPromptTokens:   contextTokens(c.system),
		ToolDefinitionTokens: contextTokens(c.definitions),
		InputTextTokens:      contextTokens(c.input),
		OutputTextTokens:     contextTokens(c.output),
		OtherTokens:          contextTokens(c.other),
		Tools:                make([]llmtypes.ToolContextUsage, 0, len(c.tools)),
		CodeMode:             codeMode,
		Estimated:            true,
	}
	b.TotalTokens = b.SystemPromptTokens + b.ToolDefinitionTokens + b.InputTextTokens + b.OutputTextTokens + b.OtherTokens
	for name, tool := range c.tools {
		usage := llmtypes.ToolContextUsage{Name: name, InputTokens: contextTokens(tool.input), OutputTokens: contextTokens(tool.output), Calls: tool.calls}
		b.Tools = append(b.Tools, usage)
		b.TotalTokens += usage.InputTokens + usage.OutputTokens
	}
	sort.Slice(b.Tools, func(i, j int) bool { return b.Tools[i].Name < b.Tools[j].Name })
	return b
}

type toolContextSize struct {
	input, output, calls int
}

type contextCounter struct {
	system, definitions, input, output, other int
	tools                                     map[string]*toolContextSize
	names                                     map[string]string
}

func (c *contextCounter) tool(name string) *toolContextSize {
	if c.tools[name] == nil {
		c.tools[name] = &toolContextSize{}
	}
	return c.tools[name]
}

func (c *contextCounter) visit(value any, namesOnly bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return
	}
	kind, role := contextString(m["type"]), contextString(m["role"])
	name, id := contextString(m["name"]), contextString(m["id"])
	arguments := m["arguments"]
	if callID := contextString(m["call_id"]); callID != "" {
		id = callID
	}
	switch kind {
	case "tool_use", "server_tool_use":
		arguments = m["input"]
	case "function": // Chat Completions tool_calls entry
		function, _ := m["function"].(map[string]any)
		name, arguments = contextString(function["name"]), function["arguments"]
	case "web_search_call":
		name, arguments = "web_search", m["action"]
	}
	isCall := kind == "tool_use" || kind == "server_tool_use" || kind == "function_call" || kind == "function" || kind == "web_search_call"
	if isCall && name != "" {
		if namesOnly {
			c.names[id] = name
		} else {
			tool := c.tool(name)
			tool.calls++
			tool.input += contextJSONSize(arguments)
		}
		return
	}
	if role == "tool" || kind == "function_call_output" || kind == "tool_result" || strings.HasSuffix(kind, "_tool_result") {
		if namesOnly {
			return
		}
		content := m["content"]
		switch {
		case role == "tool":
			id = contextString(m["tool_call_id"])
		case kind == "function_call_output":
			content = m["output"]
		default:
			id = contextString(m["tool_use_id"])
		}
		if name = c.names[id]; name != "" {
			c.tool(name).output += contextTextSize(content)
		} else {
			// Compacted/orphan results have no reliable tool name.
			c.other += contextTextSize(content)
		}
		return
	}
	if role != "" {
		content := m["content"]
		if blocks, ok := content.([]any); ok {
			for _, block := range blocks {
				c.visit(block, namesOnly)
			}
		}
		if calls, ok := m["tool_calls"].([]any); ok {
			for _, call := range calls {
				c.visit(call, namesOnly)
			}
		}
		if !namesOnly {
			// Text extraction does not traverse tool calls/results or reasoning,
			// which were classified separately above.
			size := contextTextSize(content) + len(contextString(m["refusal"]))
			switch role {
			case "system", "developer":
				c.system += size
			case "assistant":
				c.output += size
			default:
				c.input += size
			}
			c.other += len(contextString(m["reasoning_content"]))
		}
		return
	}
	if !namesOnly {
		switch kind {
		case "thinking":
			c.other += len(contextString(m["thinking"]))
		case "reasoning":
			c.other += contextTextSize(m["summary"]) + contextTextSize(m["content"])
		case "compaction", "compaction_summary":
			c.other += contextTextSize(m["content"])
		}
	}
}

// contextTextSize deliberately recognizes only textual wire content. Never walk
// arbitrary map values here: image/file/audio data and encrypted signatures can
// dwarf the real context, and display-only metadata is not model input.
func contextTextSize(value any) int {
	switch value := value.(type) {
	case string:
		return len(value)
	case []any:
		size := 0
		for _, item := range value {
			size += contextTextSize(item)
		}
		return size
	case map[string]any:
		switch contextString(value["type"]) {
		case "text", "input_text", "output_text", "summary_text", "reasoning_text":
			return len(contextString(value["text"]))
		case "refusal":
			return len(contextString(value["refusal"]))
		case "web_search_result":
			return len(contextString(value["title"])) + len(contextString(value["url"]))
		case "web_search_tool_result_error":
			return len(contextString(value["error_code"]))
		}
	}
	return 0
}

func contextString(value any) string {
	text, _ := value.(string)
	return text
}

func contextJSONSize(value any) int {
	if value == nil {
		return 0
	}
	if text, ok := value.(string); ok {
		return len(text)
	}
	raw, _ := json.Marshal(value)
	return len(raw)
}

func contextTokens(size int) int { return (size + 3) / 4 }

// SetContextBreakdown replaces the immutable snapshot exported through Usage.
// A nil value invalidates a snapshot after context changes we cannot estimate.
func (t *Thread) SetContextBreakdown(breakdown *llmtypes.ContextBreakdown) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if t.Usage == nil {
		t.Usage = &llmtypes.Usage{}
	}
	t.Usage.ContextBreakdown = cloneContextBreakdown(breakdown)
}

func cloneContextBreakdown(breakdown *llmtypes.ContextBreakdown) *llmtypes.ContextBreakdown {
	if breakdown == nil {
		return nil
	}
	clone := *breakdown
	clone.Tools = append([]llmtypes.ToolContextUsage{}, breakdown.Tools...)
	return &clone
}
