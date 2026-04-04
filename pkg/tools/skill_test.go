package tools

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/skills"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillTool_Name(t *testing.T) {
	tool := NewSkillTool(nil, true, false)
	assert.Equal(t, "skill", tool.Name())
}

func TestSkillTool_Description(t *testing.T) {
	t.Run("with no skills", func(t *testing.T) {
		tool := NewSkillTool(nil, true, false)
		desc := tool.Description()
		assert.Contains(t, desc, "Skills are currently not available")
	})

	t.Run("with skills disabled", func(t *testing.T) {
		skills := map[string]*skills.Skill{
			"test": {Name: "test", Description: "A test skill", Directory: "/test"},
		}
		tool := NewSkillTool(skills, false, false)
		desc := tool.Description()
		assert.Contains(t, desc, "Skills are currently not available")
	})

	t.Run("with skills enabled", func(t *testing.T) {
		skillsMap := map[string]*skills.Skill{
			"pdf":  {Name: "pdf", Description: "Handle PDF files", Directory: "/skills/pdf"},
			"xlsx": {Name: "xlsx", Description: "Handle Excel files", Directory: "/skills/xlsx"},
		}
		tool := NewSkillTool(skillsMap, true, false)
		desc := tool.Description()
		assert.Contains(t, desc, "### pdf")
		assert.Contains(t, desc, "Handle PDF files")
		assert.Contains(t, desc, "### xlsx")
		assert.Contains(t, desc, "Handle Excel files")
	})

	t.Run("with fs search tools disabled", func(t *testing.T) {
		tool := NewSkillTool(nil, true, true)
		desc := tool.Description()
		assert.Contains(t, desc, "file_read or fd via bash")
		assert.NotContains(t, desc, "file_read or glob_tool")
	})

	t.Run("patch avoids removed file tools", func(t *testing.T) {
		tool := NewSkillToolWithOptions(nil, true, llmtypes.ToolModePatch, false)
		desc := tool.Description()
		assert.Contains(t, desc, "locate with glob_tool and inspect via bash using sed/cat/rg")
		assert.Contains(t, desc, "update it using apply_patch")
		assert.NotContains(t, desc, "read using file_read")
		assert.NotContains(t, desc, "file_edit tool")
	})
}

func TestSkillTool_ValidateInput(t *testing.T) {
	skillsMap := map[string]*skills.Skill{
		"test": {Name: "test", Description: "A test skill", Directory: "/test"},
	}

	t.Run("valid input", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)
		err := tool.ValidateInput(nil, `{"skill_name": "test"}`)
		assert.NoError(t, err)
	})

	t.Run("missing skill_name", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)
		err := tool.ValidateInput(nil, `{}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "skill_name is required")
	})

	t.Run("skills disabled", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, false, false)
		err := tool.ValidateInput(nil, `{"skill_name": "test"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "skills are disabled")
	})

	t.Run("unknown skill", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)
		err := tool.ValidateInput(nil, `{"skill_name": "unknown"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown skill")
		assert.Contains(t, err.Error(), "test")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)
		err := tool.ValidateInput(nil, `invalid json`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid input")
	})
}

func TestSkillTool_Execute(t *testing.T) {
	skillsMap := map[string]*skills.Skill{
		"test": {
			Name:        "test",
			Description: "A test skill",
			Directory:   "/skills/test",
			Content:     "# Test Skill\n\nSome instructions here.",
		},
	}

	t.Run("successful execution", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)
		result := tool.Execute(context.Background(), nil, `{"skill_name": "test"}`)

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "Skill 'test' loaded")
		assert.Contains(t, result.AssistantFacing(), "# Skill: test")
		assert.Contains(t, result.AssistantFacing(), "/skills/test")
		assert.Contains(t, result.AssistantFacing(), "# Test Skill")
	})

	t.Run("skill not found", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)
		result := tool.Execute(context.Background(), nil, `{"skill_name": "unknown"}`)

		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "not found")
	})

	t.Run("skill already active", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)

		result1 := tool.Execute(context.Background(), nil, `{"skill_name": "test"}`)
		assert.False(t, result1.IsError())

		result2 := tool.Execute(context.Background(), nil, `{"skill_name": "test"}`)
		assert.True(t, result2.IsError())
		assert.Contains(t, result2.GetError(), "already active")
	})

	t.Run("reset active skills", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)

		tool.Execute(context.Background(), nil, `{"skill_name": "test"}`)
		assert.True(t, tool.IsActive("test"))

		tool.ResetActiveSkills()
		assert.False(t, tool.IsActive("test"))

		result := tool.Execute(context.Background(), nil, `{"skill_name": "test"}`)
		assert.False(t, result.IsError())
	})

	t.Run("invalid JSON", func(t *testing.T) {
		tool := NewSkillTool(skillsMap, true, false)
		result := tool.Execute(context.Background(), nil, `invalid`)

		assert.True(t, result.IsError())
	})
}

func TestSkillTool_TracingKVs(t *testing.T) {
	tool := NewSkillTool(nil, true, false)

	t.Run("valid input", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"skill_name": "test"}`)
		require.NoError(t, err)
		assert.Len(t, kvs, 1)
		assert.Equal(t, "skill_name", string(kvs[0].Key))
		assert.Equal(t, "test", kvs[0].Value.AsString())
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := tool.TracingKVs(`invalid`)
		assert.Error(t, err)
	})
}

func TestSkillToolResult_StructuredData(t *testing.T) {
	t.Run("successful result", func(t *testing.T) {
		result := &SkillToolResult{
			skillName: "test",
			directory: "/skills/test",
			content:   "Test content",
		}

		structured := result.StructuredData()
		assert.Equal(t, "skill", structured.ToolName)
		assert.True(t, structured.Success)
		assert.Empty(t, structured.Error)
		assert.NotNil(t, structured.Metadata)
	})

	t.Run("error result", func(t *testing.T) {
		result := &SkillToolResult{
			err: "something went wrong",
		}

		structured := result.StructuredData()
		assert.Equal(t, "skill", structured.ToolName)
		assert.False(t, structured.Success)
		assert.Equal(t, "something went wrong", structured.Error)
		assert.Nil(t, structured.Metadata)
	})
}

func TestSkillTool_GettersAndHelpers(t *testing.T) {
	skillsMap := map[string]*skills.Skill{
		"test": {Name: "test", Description: "A test skill"},
	}

	tool := NewSkillTool(skillsMap, true, false)

	assert.True(t, tool.IsEnabled())
	assert.Equal(t, skillsMap, tool.GetSkills())
	assert.NotNil(t, tool.GenerateSchema())
}
