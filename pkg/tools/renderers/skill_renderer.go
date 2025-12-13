package renderers

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SkillRenderer renders skill tool results
type SkillRenderer struct{}

// RenderCLI renders skill results in CLI format
func (r *SkillRenderer) RenderCLI(result tools.StructuredToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var meta tools.SkillMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return "Error: Invalid metadata type for skill"
	}

	return fmt.Sprintf("Skill '%s' loaded from %s", meta.SkillName, meta.Directory)
}
