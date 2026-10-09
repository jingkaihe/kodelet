package codemode

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// maxValidationDetails bounds the host diagnostics kept for one failure.
const maxValidationDetails = 8

// OutputValidationError reports an invalid output schema or mismatched data.
// Its message is safe to show scripts. Details are for host logs only: the
// compiler's diagnosis of a schema, or the instance and keyword locations of a
// mismatch, never instance values.
type OutputValidationError struct {
	message string
	Details []string
}

func (e *OutputValidationError) Error() string { return e.message }

func outputValidationError(message string, details ...string) error {
	return &OutputValidationError{message: message, Details: details}
}

// ValidateOutputData checks an already-serialized, effective tool payload. Null
// is always permitted because result hooks can remove structured data. Schemas
// describe only data, not the reply envelope or presentation metadata.
func ValidateOutputData(schema map[string]any, data json.RawMessage) error {
	if len(schema) == 0 || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	// Normalize Go-built schemas (including []string and native integers) while
	// retaining JSON number precision. Never put validator errors in the message:
	// they can include raw instance values, schema literals, or resource URLs.
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return outputValidationError("tool outputSchema is not valid JSON; the tool will not be retried", err.Error())
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return outputValidationError("tool outputSchema is not valid JSON; the tool will not be retried", err.Error())
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	// Local refs and the library's embedded metaschemas remain available. No
	// extension-provided reference may load a file or make a network request.
	compiler.UseLoader(nil)
	const location = "urn:kodelet:tool-output"
	if err := compiler.AddResource(location, document); err != nil {
		return outputValidationError("tool outputSchema could not be registered; the tool will not be retried", err.Error())
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return outputValidationError(
			"tool outputSchema is invalid or requires an external resource; the tool will not be retried",
			err.Error(),
		)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return outputValidationError("child reply data is not valid JSON; the tool will not be retried")
	}
	if err := compiled.Validate(value); err != nil {
		return outputValidationError(
			"child reply data does not match its declared outputSchema; the tool will not be retried",
			mismatchLocations(err)...,
		)
	}
	return nil
}

// mismatchLocations lists "instance location: schema keyword" for the deepest
// failures, so tool authors can find a mismatch without logging its values.
func mismatchLocations(err error) []string {
	var root *jsonschema.ValidationError
	if !errors.As(err, &root) {
		return nil
	}
	var details []string
	var walk func(*jsonschema.ValidationError)
	walk = func(failure *jsonschema.ValidationError) {
		if len(details) >= maxValidationDetails {
			return
		}
		if len(failure.Causes) > 0 {
			for _, cause := range failure.Causes {
				walk(cause)
			}
			return
		}
		keyword := ""
		if failure.ErrorKind != nil {
			keyword = strings.Join(failure.ErrorKind.KeywordPath(), "/")
		}
		// Missing names come from the schema, not the instance, so they are safe.
		if required, ok := failure.ErrorKind.(*kind.Required); ok {
			keyword += " " + strings.Join(required.Missing, ", ")
		}
		details = append(details, "/"+strings.Join(failure.InstanceLocation, "/")+": "+keyword)
	}
	walk(root)
	return details
}
