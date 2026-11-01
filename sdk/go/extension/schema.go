package extension

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
)

var (
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	anyMapType     = reflect.TypeFor[map[string]any]()
	anyType        = reflect.TypeFor[any]()
)

// inputSchema is an advertised JSON Schema plus an optional validator.
type inputSchema struct {
	document map[string]any
	resolved *jsonschema.Resolved
}

func isRawInputType(t reflect.Type) bool {
	return t == rawMessageType || t == anyMapType || t == anyType
}

// inferInputSchema derives a schema from a Go type. Nullable type unions
// produced for slices and pointers are collapsed to their non-null type so the
// result matches ordinary hand-written tool schemas.
func inferInputSchema(t reflect.Type) (*inputSchema, error) {
	schema, err := jsonschema.ForType(t, &jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{rawMessageType: {}},
	})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("schema for %s is not an object", t)
	}
	normalizeSchema(document)
	return newValidatedSchema(document)
}

func newValidatedSchema(document map[string]any) (*inputSchema, error) {
	data, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var normalized jsonschema.Schema
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}
	resolved, err := normalized.Resolve(nil)
	if err != nil {
		return nil, err
	}
	return &inputSchema{document: document, resolved: resolved}, nil
}

var schemaMapKeywords = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}

var schemaKeywords = []string{
	"items", "additionalProperties", "not", "if", "then", "else", "contains",
	"propertyNames", "unevaluatedItems", "unevaluatedProperties", "additionalItems",
}

var schemaListKeywords = []string{"allOf", "anyOf", "oneOf", "prefixItems"}

func normalizeSchema(node any) {
	schema, ok := node.(map[string]any)
	if !ok {
		return
	}
	if types, ok := schema["type"].([]any); ok {
		var nonNull []string
		for _, value := range types {
			name, _ := value.(string)
			if name != "null" {
				nonNull = append(nonNull, name)
			}
		}
		if len(nonNull) == 1 {
			schema["type"] = nonNull[0]
		}
	}
	for _, keyword := range schemaKeywords {
		normalizeSchema(schema[keyword])
	}
	for _, keyword := range schemaListKeywords {
		if items, ok := schema[keyword].([]any); ok {
			for _, item := range items {
				normalizeSchema(item)
			}
		}
	}
	for _, keyword := range schemaMapKeywords {
		if items, ok := schema[keyword].(map[string]any); ok {
			for _, item := range items {
				normalizeSchema(item)
			}
		}
	}
}

func (s *inputSchema) validate(raw json.RawMessage) error {
	if s == nil || s.resolved == nil {
		return nil
	}
	var instance any = map[string]any{}
	if jsonrpc.HasValue(raw) {
		if err := json.Unmarshal(raw, &instance); err != nil {
			return err
		}
	}
	return s.resolved.Validate(instance)
}

func decodeInput[In any](raw json.RawMessage) (In, error) {
	var input In
	if !jsonrpc.HasValue(raw) {
		if reflect.TypeFor[In]() == anyMapType {
			return any(map[string]any{}).(In), nil
		}
		return input, nil
	}
	err := json.Unmarshal(raw, &input)
	return input, err
}
