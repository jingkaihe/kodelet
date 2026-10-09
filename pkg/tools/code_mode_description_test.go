package tools

import (
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestCodeModeNotesStayOutOfDirectDescriptions(t *testing.T) {
	for _, tool := range []tooltypes.Tool{NewBashTool(nil, false), &FileReadTool{}, &BrowserTool{}, &ViewImageTool{}} {
		assert.NotContains(t, tool.Description(), "reply.", tool.Name())
		assert.Contains(t, tooltypes.CodeModeDescriptionForTool(tool), "In code mode, ", tool.Name())
	}
}
