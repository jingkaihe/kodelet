package codemode

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// toolIndexMaxBytes bounds the code_execute tool index, about 2,000 tokens.
	toolIndexMaxBytes     = 8 << 10
	summaryMaxRunes       = 120
	signatureMaxRunes     = 400
	signatureMaxFields    = 16
	defaultMaxRunes       = 40
	groupLabelMaxRunes    = 100
	signatureDescribeHint = "; call catalog.describe(name) for full contract"
)

const toolIndexHeader = "Callable tools, listed under their catalog group. This list is complete. " +
	"Call each tool by the exact name that starts its line, as tools[name](input); " +
	"group headings are for catalog.list({group}) and are never part of a tool name " +
	"(tools.bash(...), not tools[\"group/bash\"](...)). " +
	"Signatures show input fields, allowed values, defaults (= value), and ToolReply<T> returns: " +
	"{data: T | null, text: string, attachments: ArtifactRef[], truncated: boolean}. " +
	"outputSchema describes data only; policy hooks may clear data. " +
	"Call catalog.describe(name) for a tool's full schemas, field descriptions, and return semantics."

// Summary returns a tool's one-line summary: its explicit Short, or else the
// first sentence of its description, on one line and capped in length.
func Summary(definition Definition) string {
	if short := oneLine(definition.Short); short != "" {
		return capRunes(short, summaryMaxRunes)
	}
	return capRunes(firstSentence(definition.Description), summaryMaxRunes)
}

// ToolIndex renders every callable tool for the code_execute description.
// Built-in tools (no group) come first with signatures and summaries, then each
// group in name order. Every tool is named; within the byte budget, groups take
// turns upgrading their cheapest remaining tool to a full line (signature and
// summary), so each group is represented before any group is complete. If even
// the names do not fit, the largest groups are reduced to a count. Built-in
// tools are never reduced. The output is deterministic for prompt caching.
func ToolIndex(definitions []Definition) string {
	seen := make(map[string]bool, len(definitions))
	byGroup := make(map[string][]Definition)
	for _, definition := range definitions {
		if definition.Name == "" || seen[definition.Name] {
			continue
		}
		seen[definition.Name] = true
		byGroup[definition.Group] = append(byGroup[definition.Group], definition)
	}
	if len(seen) == 0 {
		return "No other tools are callable in this turn."
	}
	names := make([]string, 0, len(byGroup))
	for group := range byGroup {
		if group != "" {
			names = append(names, group)
		}
	}
	slices.Sort(names)
	if _, ok := byGroup[""]; ok {
		names = append([]string{""}, names...)
	}

	groups := make([]*indexGroup, len(names))
	total := len(toolIndexHeader)
	for i, name := range names {
		groups[i] = newIndexGroup(name, byGroup[name])
		total += len(groups[i].render()) + 1
	}
	// Every tool is at least named. If the names alone exceed the budget,
	// reduce the largest groups to a count.
	for total > toolIndexMaxBytes {
		largest := -1
		for i, group := range groups {
			if group.builtIn || group.countOnly {
				continue
			}
			if largest < 0 || len(group.render()) > len(groups[largest].render()) {
				largest = i
			}
		}
		if largest < 0 {
			break
		}
		before := len(groups[largest].render())
		groups[largest].countOnly = true
		total += len(groups[largest].render()) - before
	}
	// Round robin: each group in turn upgrades its cheapest remaining tool to a
	// full line while it fits. A group whose next tool does not fit drops out.
	active := make([]*indexGroup, 0, len(groups))
	for _, group := range groups {
		if !group.builtIn && !group.countOnly {
			active = append(active, group)
		}
	}
	for len(active) > 0 {
		next := active[:0]
		for _, group := range active {
			before := len(group.render())
			group.full[group.order[group.upgraded]] = true
			after := len(group.render())
			if total+after-before > toolIndexMaxBytes {
				group.full[group.order[group.upgraded]] = false
				continue
			}
			total += after - before
			group.upgraded++
			if group.upgraded < len(group.order) {
				next = append(next, group)
			}
		}
		active = next
	}

	var output strings.Builder
	output.WriteString(toolIndexHeader)
	for _, group := range groups {
		output.WriteByte('\n')
		output.WriteString(group.render())
	}
	return output.String()
}

// indexGroup tracks which of a group's tools are listed with full lines.
type indexGroup struct {
	group     string
	label     string
	builtIn   bool
	countOnly bool
	members   []Definition // sorted by name for display
	lines     []string     // full line per member
	full      []bool       // member is shown with its full line
	order     []int        // members by full-line cost, cheapest first
	upgraded  int
}

func newIndexGroup(group string, members []Definition) *indexGroup {
	slices.SortFunc(members, func(a, b Definition) int { return strings.Compare(a.Name, b.Name) })
	entry := &indexGroup{
		group:   group,
		label:   "Built-in tools",
		builtIn: group == "",
		members: members,
		lines:   make([]string, len(members)),
		full:    make([]bool, len(members)),
		order:   make([]int, len(members)),
	}
	if !entry.builtIn {
		// Spell the heading as a group so it is not read as a name prefix.
		entry.label = "Group " + capRunes(oneLine(group), groupLabelMaxRunes)
	}
	for i, definition := range members {
		line := "  " + Signature(definition)
		if summary := Summary(definition); summary != "" {
			line += " — " + summary
		}
		entry.lines[i] = line
		entry.full[i] = entry.builtIn
		entry.order[i] = i
	}
	slices.SortStableFunc(entry.order, func(a, b int) int { return len(entry.lines[a]) - len(entry.lines[b]) })
	return entry
}

func (g *indexGroup) render() string {
	count := strconv.Itoa(len(g.members)) + " tool"
	if len(g.members) != 1 {
		count += "s"
	}
	if g.countOnly {
		quotedGroup, _ := json.Marshal(g.group)
		return g.label + " (" + count + "): browse with catalog.list({group: " + string(quotedGroup) + "})"
	}
	var lines []string
	var named []string
	for i, definition := range g.members {
		if g.full[i] {
			lines = append(lines, g.lines[i])
		} else {
			named = append(named, indexIdentifier(definition.Name))
		}
	}
	const describeHint = "call catalog.describe(name) for details"
	switch {
	case len(named) == 0:
		return g.label + ":\n" + strings.Join(lines, "\n")
	case len(lines) == 0:
		return g.label + " (" + count + ", names only; " + describeHint + "): " + strings.Join(named, ", ")
	default:
		return g.label + " (" + count + ", " + strconv.Itoa(len(lines)) + " shown in full):\n" +
			strings.Join(lines, "\n") + "\n  Also callable (" + describeHint + "): " + strings.Join(named, ", ")
	}
}

// Signature renders input fields and a ToolReply<T> return type from the data
// schema. Oversized signatures drop input detail before output detail, always
// retaining a return type and an explicit discovery hint when abbreviated.
func Signature(definition Definition) string {
	name := indexIdentifier(definition.Name)
	// Normalize native Go schema values just as Describe does, so declarations
	// agree for []string unions, enums, and required fields as well as JSON input.
	outputType := catalogSchemaType(cloneCatalogSchema(definition.OutputSchema), 0)
	output := " → ToolReply<" + outputType + ">"
	candidates := []string{
		name + "(" + indexInputType(definition.InputSchema, true) + ")" + output,
		name + "(" + indexInputType(definition.InputSchema, false) + ")" + output,
		name + "(…)" + output,
		name + "(…) → ToolReply<unknown>",
	}
	for i, signature := range candidates {
		if i > 0 || strings.Contains(signature, "…") || outputType == "unknown" && len(definition.OutputSchema) > 0 {
			signature += signatureDescribeHint
		}
		if utf8.RuneCountInString(signature) <= signatureMaxRunes {
			return signature
		}
	}
	// An unusually long registered name must remain exact and callable.
	return candidates[len(candidates)-1] + signatureDescribeHint
}

func indexInputType(schema map[string]any, typed bool) string {
	if len(schema) == 0 {
		return "{}"
	}
	properties, hasProperties := schema["properties"].(map[string]any)
	if !hasProperties {
		if kind, _ := schema["type"].(string); kind == "object" {
			return "{}"
		}
		return indexType(schema, 0)
	}
	required := schemaRequired(schema)
	isRequired := make(map[string]bool, len(required))
	ordered := make([]string, 0, len(properties))
	for _, name := range required {
		if _, exists := properties[name]; exists && !isRequired[name] {
			isRequired[name] = true
			ordered = append(ordered, name)
		}
	}
	optional := make([]string, 0, len(properties))
	for name := range properties {
		if !isRequired[name] {
			optional = append(optional, name)
		}
	}
	slices.Sort(optional)
	ordered = append(ordered, optional...)
	fields := make([]string, 0, min(len(ordered), signatureMaxFields)+1)
	for i, name := range ordered {
		if i == signatureMaxFields {
			fields = append(fields, "…")
			break
		}
		field := indexIdentifier(name)
		if !isRequired[name] {
			field += "?"
		}
		property, _ := properties[name].(map[string]any)
		if typed {
			field += ": " + indexType(property, 1)
			if value, exists := property["default"]; exists && value != nil {
				if literal := catalogLiteral(value); literal != "unknown" && utf8.RuneCountInString(literal) <= defaultMaxRunes {
					field += " = " + literal
				}
			}
		}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return "{}"
	}
	separator := ", "
	if typed {
		separator = "; "
	}
	return "{ " + strings.Join(fields, separator) + " }"
}

// indexType renders a compact type: literal unions for enums and constants,
// unions for anyOf, oneOf, and type lists, arrays, and shallow objects.
func indexType(schema map[string]any, depth int) string {
	if len(schema) == 0 || depth > 3 {
		return "unknown"
	}
	if value, exists := schema["const"]; exists {
		return catalogLiteral(value)
	}
	if values, ok := schema["enum"].([]any); ok && len(values) > 0 && len(values) <= 16 {
		literals := make([]string, 0, len(values))
		for _, value := range values {
			literals = append(literals, catalogLiteral(value))
		}
		if !slices.Contains(literals, "unknown") {
			return strings.Join(literals, " | ")
		}
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if members, ok := schema[key].([]any); ok {
			union := make([]string, 0, len(members))
			for _, member := range members {
				memberSchema, _ := member.(map[string]any)
				union = append(union, indexType(memberSchema, depth+1))
			}
			return joinUnion(union)
		}
	}
	if kinds, ok := schema["type"].([]any); ok {
		union := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			member := make(map[string]any, len(schema))
			for key, value := range schema {
				member[key] = value
			}
			member["type"] = kind
			union = append(union, indexType(member, depth+1))
		}
		return joinUnion(union)
	}
	kind, _ := schema["type"].(string)
	switch kind {
	case "string", "boolean", "null":
		return kind
	case "number", "integer":
		return "number"
	case "array":
		items, _ := schema["items"].(map[string]any)
		item := indexType(items, depth+1)
		if strings.Contains(item, " | ") {
			return "(" + item + ")[]"
		}
		return item + "[]"
	case "object":
		if _, ok := schema["properties"].(map[string]any); !ok || depth >= 2 {
			return "object"
		}
		return indexInputType(schema, true)
	default:
		return "unknown"
	}
}

func joinUnion(members []string) string {
	unique := make([]string, 0, len(members))
	for _, member := range members {
		if !slices.Contains(unique, member) {
			unique = append(unique, member)
		}
	}
	if len(unique) == 0 || len(unique) > 8 || slices.Contains(unique, "unknown") {
		return "unknown"
	}
	return strings.Join(unique, " | ")
}

// schemaRequired accepts both decoded JSON ([]any) and Go-built ([]string) lists.
func schemaRequired(schema map[string]any) []string {
	switch names := schema["required"].(type) {
	case []string:
		return names
	case []any:
		result := make([]string, 0, len(names))
		for _, value := range names {
			if name, ok := value.(string); ok {
				result = append(result, name)
			}
		}
		return result
	}
	return nil
}

// indexIdentifier prints a name as-is when it is a JavaScript identifier, and
// JSON-quoted otherwise, as it must be written inside tools[...] or an object.
func indexIdentifier(name string) string {
	for i, char := range name {
		if char == '_' || char == '$' || char < utf8.RuneSelf && unicode.IsLetter(char) ||
			i > 0 && char < utf8.RuneSelf && unicode.IsDigit(char) {
			continue
		}
		quoted, _ := json.Marshal(name)
		return string(quoted)
	}
	if name == "" {
		return `""`
	}
	return name
}

// firstSentence returns the first sentence of a description's first paragraph,
// skipping leading Markdown headings.
func firstSentence(description string) string {
	var paragraph []string
	for _, line := range strings.Split(description, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if len(paragraph) == 0 {
				continue
			}
			break
		}
		if trimmed == "" {
			if len(paragraph) == 0 {
				continue
			}
			break
		}
		paragraph = append(paragraph, trimmed)
	}
	text := []rune(strings.TrimLeft(oneLine(strings.Join(paragraph, " ")), "-*> "))
	for i := 0; i+2 < len(text); i++ {
		if (text[i] == '.' || text[i] == '!' || text[i] == '?') && text[i+1] == ' ' && !unicode.IsLower(text[i+2]) {
			return string(text[:i+1])
		}
	}
	return string(text)
}

// oneLine collapses whitespace and removes control characters.
func oneLine(text string) string {
	return strings.Join(strings.FieldsFunc(text, func(char rune) bool {
		return unicode.IsSpace(char) || unicode.IsControl(char)
	}), " ")
}

func capRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := limit - 1
	if space := strings.LastIndex(string(runes[:cut]), " "); space > 0 && utf8.RuneCountInString(string(runes[:cut])[:space]) > limit-30 {
		return strings.TrimRight(string(runes[:cut])[:space], " ,;:") + "…"
	}
	return strings.TrimRight(string(runes[:cut]), " ") + "…"
}
