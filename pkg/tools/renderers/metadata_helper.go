package renderers

import (
	"reflect"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// extractMetadata is a helper that handles both pointer and value type assertions
// This is necessary because JSON unmarshaling creates value types, while
// direct creation uses pointer types
func extractMetadata(metadata tools.ToolMetadata, target interface{}) bool {
	if metadata == nil {
		return false
	}

	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Ptr || targetValue.IsNil() {
		return false
	}

	targetElem := targetValue.Elem()
	metadataValue := reflect.ValueOf(metadata)

	// If metadata is a pointer, dereference it
	if metadataValue.Kind() == reflect.Ptr && !metadataValue.IsNil() {
		metadataValue = metadataValue.Elem()
	}

	// Check if the types match (comparing the base types, not pointer vs value)
	if targetElem.Type() != metadataValue.Type() {
		return false
	}

	// Set the target to the metadata value
	targetElem.Set(metadataValue)
	return true
}
