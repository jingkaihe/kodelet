package codemode

import (
	"bytes"
	"encoding/json"

	"github.com/pkg/errors"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidateOutputData checks an already-serialized, effective tool payload. Null
// is always permitted because result hooks can remove structured data. Schemas
// describe only data, not the reply envelope or presentation metadata.
func ValidateOutputData(schema map[string]any, data json.RawMessage) error {
	if len(schema) == 0 || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	// Normalize Go-built schemas (including []string and native integers) while
	// retaining JSON number precision. Never expose validator errors: they can
	// include raw instance values, schema literals, or external resource URLs.
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return errors.New("tool outputSchema is not valid JSON; the tool will not be retried")
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return errors.New("tool outputSchema is not valid JSON; the tool will not be retried")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	// Local refs and the library's embedded metaschemas remain available. No
	// extension-provided reference may load a file or make a network request.
	compiler.UseLoader(nil)
	const location = "urn:kodelet:tool-output"
	if err := compiler.AddResource(location, document); err != nil {
		return errors.New("tool outputSchema could not be registered; the tool will not be retried")
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return errors.New("tool outputSchema is invalid or requires an external resource; the tool will not be retried")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return errors.New("child reply data is not valid JSON; the tool will not be retried")
	}
	if err := compiled.Validate(value); err != nil {
		return errors.New("child reply data does not match its declared outputSchema; the tool will not be retried")
	}
	return nil
}
