package codemode

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/pkg/errors"
)

const (
	catalogVersion       = "bm25-v1"
	catalogDefaultLimit  = 20
	catalogMaxLimit      = 100
	catalogMaxQueryBytes = 4096
	catalogMaxFieldBytes = 8192
)

// Definition describes one already-authorized tool. Names are exact registered names.
type Definition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Group       string `json:"group,omitempty"`
	// Short is an explicit one-line summary. Without one, listings use the
	// first sentence of Description.
	Short        string         `json:"short,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

// CatalogOptions controls exact group filtering and bounded pagination.
type CatalogOptions struct {
	Group  string `json:"group,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// ToolSummary is a compact discovery result; full schemas require Describe.
type ToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Group       string `json:"group,omitempty"`
}

// Page contains a bounded list of tools and an opaque continuation cursor.
type Page struct {
	Tools      []ToolSummary `json:"tools"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// Description contains the original schemas and a conservative TypeScript declaration.
type Description struct {
	Definition
	Declaration string `json:"declaration"`
}

// toolReplyDeclaration keeps Describe self-contained rather than requiring the
// caller to infer the envelope or media behavior from the data schema.
const toolReplyDeclaration = `// outputSchema describes ToolReply.data, not the whole reply.
// Policy hooks may clear data even when an output schema is declared.
interface ToolReply<T = unknown> {
  data: T | null;
  /** Textual output or supplementary information; empty when unused. */
  text: string;
  attachments: ArtifactRef[];
  /** Content was shortened or omitted by output limits; not pagination. */
  truncated: boolean;
}

// Current artifacts are images. References contain no image bytes.
// Use emit.image(ref) for pixels or emit.artifact(ref) for retention only.
// Returning these descriptors as JSON does not select media.
interface ArtifactRef {
  type: "image";
  /** Present after successful ingestion; check error before selecting media. */
  artifactId?: string;
  shortCode?: string;
  viewUrl?: string;
  filename?: string;
  mimeType?: string;
  alt?: string;
  width?: number;
  height?: number;
  size?: number;
  error?: string;
}

// Tool failures reject with this serializable error; no implicit retry or rollback.
interface ToolError<T = unknown> extends Error {
  kind: "blocked" | "invalid_input" | "tool_error" | "transport" | "cancelled" | "limit" | "invalid_output";
  tool: string;
  callId?: string;
  /** completed does not imply success or absence of side effects. */
  outcome: "not_started" | "completed" | "unknown";
  result?: ToolReply<T>;
  toJSON(): object;
}

`

type catalogField struct {
	terms  map[string]int
	length int
}

type catalogDocument struct {
	definition Definition
	fields     [4]catalogField
}

type catalogFieldStats struct {
	frequency map[string]int
	average   float64
}

// Catalog is an immutable, in-memory snapshot safe for concurrent discovery.
// Authorization is supplied by its caller, never inferred from discovery requests.
type Catalog struct {
	documents   []catalogDocument
	byName      map[string]int
	stats       [4]catalogFieldStats
	fingerprint string
}

// NewCatalog snapshots authorized definitions. Duplicate names retain the first definition.
// Schemas must contain JSON values; unsupported non-JSON schemas degrade to unknown.
func NewCatalog(definitions []Definition) *Catalog {
	catalog := &Catalog{byName: make(map[string]int)}
	for _, definition := range definitions {
		if _, exists := catalog.byName[definition.Name]; exists {
			continue
		}
		definition.InputSchema = cloneCatalogSchema(definition.InputSchema)
		definition.OutputSchema = cloneCatalogSchema(definition.OutputSchema)
		catalog.byName[definition.Name] = len(catalog.documents)
		catalog.documents = append(catalog.documents, catalogDocument{definition: definition})
	}
	slices.SortFunc(catalog.documents, func(a, b catalogDocument) int {
		return strings.Compare(a.definition.Name, b.definition.Name)
	})
	for field := range catalog.stats {
		catalog.stats[field].frequency = make(map[string]int)
	}
	canonical := make([]Definition, 0, len(catalog.documents))
	for i := range catalog.documents {
		document := &catalog.documents[i]
		definition := document.definition
		catalog.byName[definition.Name] = i
		canonical = append(canonical, definition)
		fields := [4]string{
			definition.Name, definition.Group, definition.Description,
			catalogPropertyText(definition.InputSchema),
		}
		for field, text := range fields {
			if len(text) > catalogMaxFieldBytes {
				text = text[:catalogMaxFieldBytes]
			}
			terms := catalogTokens(text)
			document.fields[field] = catalogField{terms: make(map[string]int), length: len(terms)}
			for _, term := range terms {
				document.fields[field].terms[term]++
			}
			for term := range document.fields[field].terms {
				catalog.stats[field].frequency[term]++
			}
			catalog.stats[field].average += float64(len(terms))
		}
	}
	if len(catalog.documents) != 0 {
		for field := range catalog.stats {
			catalog.stats[field].average /= float64(len(catalog.documents))
		}
	}
	payload, _ := json.Marshal(canonical)
	catalog.fingerprint = catalogDigest(payload)
	return catalog
}

// List enumerates every authorized tool in stable registered-name order.
func (c *Catalog) List(options CatalogOptions) (Page, error) {
	indices := make([]int, 0, len(c.documents))
	for i, document := range c.documents {
		if options.Group == "" || document.definition.Group == options.Group {
			indices = append(indices, i)
		}
	}
	return c.page(indices, "list", "", options)
}

// Search ranks partial lexical matches using field-weighted BM25, exact names first.
// It does not pad results with unrelated tools or expose scores as probabilities.
func (c *Catalog) Search(query string, options CatalogOptions) (Page, error) {
	if len(query) > catalogMaxQueryBytes {
		return Page{}, errors.New("catalog search query exceeds 4096 bytes")
	}
	terms := catalogTokens(query)
	slices.Sort(terms)
	terms = slices.Compact(terms)
	query = strings.ToLower(strings.TrimSpace(query))
	type match struct {
		index int
		exact bool
		score float64
	}
	matches := make([]match, 0)
	for i, document := range c.documents {
		if options.Group != "" && document.definition.Group != options.Group {
			continue
		}
		exact := query != "" && strings.EqualFold(query, document.definition.Name)
		score := c.score(document, terms)
		if exact || score > 0 {
			matches = append(matches, match{index: i, exact: exact, score: score})
		}
	}
	slices.SortFunc(matches, func(a, b match) int {
		if a.exact != b.exact {
			if a.exact {
				return -1
			}
			return 1
		}
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return strings.Compare(c.documents[a.index].definition.Name, c.documents[b.index].definition.Name)
	})
	indices := make([]int, len(matches))
	for i, match := range matches {
		indices[i] = match.index
	}
	return c.page(indices, "search", query+"\x00"+strings.Join(terms, "\x00"), options)
}

func (c *Catalog) score(document catalogDocument, terms []string) float64 {
	const k1, b = 1.2, 0.75
	weights := [4]float64{5, 2, 2, 1}
	var score float64
	for field, value := range document.fields {
		stats := c.stats[field]
		if stats.average == 0 {
			continue
		}
		for _, term := range terms {
			frequency := float64(value.terms[term])
			if frequency == 0 {
				continue
			}
			documentFrequency := float64(stats.frequency[term])
			idf := math.Log1p((float64(len(c.documents)) - documentFrequency + 0.5) / (documentFrequency + 0.5))
			normalizer := frequency + k1*(1-b+b*float64(value.length)/stats.average)
			score += weights[field] * idf * frequency * (k1 + 1) / normalizer
		}
	}
	return score
}

// Describe returns only an exact authorized name, with defensive schema copies.
func (c *Catalog) Describe(name string) (Description, error) {
	index, ok := c.byName[name]
	if !ok {
		return Description{}, errors.New("tool is not available in this catalog")
	}
	definition := c.documents[index].definition
	definition.InputSchema = cloneCatalogSchema(definition.InputSchema)
	definition.OutputSchema = cloneCatalogSchema(definition.OutputSchema)
	quotedName, _ := json.Marshal(definition.Name)
	input := catalogSchemaType(definition.InputSchema, 0)
	output := catalogSchemaType(definition.OutputSchema, 0)
	return Description{
		Definition: definition,
		Declaration: toolReplyDeclaration + "declare const tools: {\n  " + string(quotedName) +
			": (input: " + input + ") => Promise<ToolReply<" + output + ">>;\n};",
	}, nil
}

type catalogCursor struct {
	Key    string `json:"key"`
	Offset int    `json:"offset"`
}

func (c *Catalog) page(indices []int, operation, query string, options CatalogOptions) (Page, error) {
	if options.Limit < 0 {
		return Page{}, errors.New("catalog page limit must not be negative")
	}
	limit := options.Limit
	if limit == 0 {
		limit = catalogDefaultLimit
	}
	limit = min(limit, catalogMaxLimit)
	keyJSON, _ := json.Marshal([]string{catalogVersion, c.fingerprint, operation, query, options.Group})
	key := catalogDigest(keyJSON)
	offset := 0
	if options.Cursor != "" {
		if len(options.Cursor) > 1024 {
			return Page{}, errors.New("invalid catalog cursor")
		}
		payload, err := base64.RawURLEncoding.DecodeString(options.Cursor)
		if err != nil {
			return Page{}, errors.Wrap(err, "invalid catalog cursor")
		}
		var cursor catalogCursor
		if err := json.Unmarshal(payload, &cursor); err != nil {
			return Page{}, errors.Wrap(err, "invalid catalog cursor")
		}
		if cursor.Key != key {
			return Page{}, errors.New("catalog cursor does not match the current catalog, query, or filters")
		}
		if cursor.Offset < 0 || cursor.Offset > len(indices) {
			return Page{}, errors.New("invalid catalog cursor offset")
		}
		offset = cursor.Offset
	}
	end := offset + min(limit, len(indices)-offset)
	page := Page{Tools: make([]ToolSummary, 0, end-offset)}
	for _, index := range indices[offset:end] {
		definition := c.documents[index].definition
		// Listings use the same one-line summary as the code_execute tool index,
		// so a tool never appears with two different summaries.
		page.Tools = append(page.Tools, ToolSummary{
			Name:        definition.Name,
			Description: Summary(definition),
			Group:       definition.Group,
		})
	}
	if end < len(indices) {
		payload, _ := json.Marshal(catalogCursor{Key: key, Offset: end})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(payload)
	}
	return page, nil
}

func catalogDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func cloneCatalogSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	payload, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var cloned map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&cloned); err != nil {
		return nil
	}
	return cloned
}

func catalogTokens(text string) []string {
	runes := []rune(text)
	var words []string
	start := 0
	for i, current := range runes {
		if !unicode.IsLetter(current) && !unicode.IsNumber(current) {
			if start < i {
				words = append(words, strings.ToLower(string(runes[start:i])))
			}
			start = i + 1
			continue
		}
		if i > start && unicode.IsUpper(current) &&
			(unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			words = append(words, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	if start < len(runes) {
		words = append(words, strings.ToLower(string(runes[start:])))
	}
	return words
}

func catalogPropertyText(schema map[string]any) string {
	var text strings.Builder
	var visit func(map[string]any, int)
	visit = func(schema map[string]any, depth int) {
		if depth > 8 || text.Len() >= catalogMaxFieldBytes {
			return
		}
		properties, _ := schema["properties"].(map[string]any)
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if text.Len() >= catalogMaxFieldBytes {
				break
			}
			property, _ := properties[name].(map[string]any)
			description, _ := property["description"].(string)
			for _, part := range []string{name, description} {
				text.WriteString(part[:min(len(part), catalogMaxFieldBytes-text.Len())])
				if text.Len() < catalogMaxFieldBytes {
					text.WriteByte(' ')
				}
			}
			visit(property, depth+1)
		}
		items, _ := schema["items"].(map[string]any)
		if len(items) != 0 {
			visit(items, depth+1)
		}
	}
	visit(schema, 0)
	return text.String()
}

func catalogSchemaType(schema map[string]any, depth int) string {
	if len(schema) == 0 || depth > 8 {
		return "unknown"
	}
	for _, key := range []string{"$ref", "$dynamicRef", "allOf", "not", "if", "patternProperties", "prefixItems"} {
		if _, exists := schema[key]; exists {
			return "unknown"
		}
	}
	// anyOf and oneOf of representable members become a union, such as
	// string | null; anything else stays unknown.
	for _, key := range []string{"anyOf", "oneOf"} {
		members, exists := schema[key]
		if !exists {
			continue
		}
		list, ok := members.([]any)
		if !ok || len(list) == 0 || len(list) > 16 {
			return "unknown"
		}
		union := make([]string, 0, len(list))
		for _, member := range list {
			memberSchema, _ := member.(map[string]any)
			typeName := catalogSchemaType(memberSchema, depth+1)
			if typeName == "unknown" {
				return "unknown"
			}
			if !slices.Contains(union, typeName) {
				union = append(union, typeName)
			}
		}
		return strings.Join(union, " | ")
	}
	if value, exists := schema["const"]; exists {
		return catalogLiteral(value)
	}
	if values, ok := schema["enum"].([]any); ok {
		if len(values) == 0 || len(values) > 32 {
			return "unknown"
		}
		literals := make([]string, 0, len(values))
		for _, value := range values {
			literal := catalogLiteral(value)
			if literal == "unknown" {
				return "unknown"
			}
			literals = append(literals, literal)
		}
		return strings.Join(literals, " | ")
	}
	if types, ok := schema["type"].([]any); ok {
		if len(types) == 0 || len(types) > 7 {
			return "unknown"
		}
		union := make([]string, 0, len(types))
		for _, kind := range types {
			member := map[string]any{}
			for key, value := range schema {
				member[key] = value
			}
			member["type"] = kind
			typeName := catalogSchemaType(member, depth+1)
			if typeName == "unknown" {
				return "unknown"
			}
			union = append(union, typeName)
		}
		return strings.Join(union, " | ")
	}
	kind, _ := schema["type"].(string)
	switch kind {
	case "string", "boolean", "null":
		return kind
	case "number", "integer":
		return "number"
	case "array":
		items, _ := schema["items"].(map[string]any)
		return "Array<" + catalogSchemaType(items, depth+1) + ">"
	case "object":
		properties, _ := schema["properties"].(map[string]any)
		if len(properties) > 64 {
			return "unknown"
		}
		required := make(map[string]bool)
		for _, name := range schemaRequired(schema) {
			required[name] = true
		}
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		for name := range required {
			if _, exists := properties[name]; !exists {
				names = append(names, name)
			}
		}
		if len(names) > 64 {
			return "unknown"
		}
		slices.Sort(names)
		fields := make([]string, 0, len(names)+1)
		for _, name := range names {
			property, _ := properties[name].(map[string]any)
			quotedName, _ := json.Marshal(name)
			optional := "?"
			if required[name] {
				optional = ""
			}
			fields = append(fields, string(quotedName)+optional+": "+catalogSchemaType(property, depth+1)+";")
		}
		if schema["additionalProperties"] != false {
			fields = append(fields, "[key: string]: unknown;")
		}
		result := "{ " + strings.Join(fields, " ") + " }"
		if len(result) > 16384 {
			return "unknown"
		}
		return result
	default:
		return "unknown"
	}
}

func catalogLiteral(value any) string {
	switch value.(type) {
	// Go-built schemas can carry native integers; decoded JSON uses float64 or json.Number.
	case nil, string, float64, float32, json.Number, bool,
		int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		payload, err := json.Marshal(value)
		if err == nil && len(payload) <= 1024 {
			return string(payload)
		}
	}
	return "unknown"
}
