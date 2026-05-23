package plugins

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSkillPluginAccessors(t *testing.T) {
	dir := filepath.Join("tmp", "skills", "review")

	plugin := NewSkillPlugin("review", "Review code", dir)

	assert.Equal(t, "review", plugin.Name())
	assert.Equal(t, "Review code", plugin.Description())
	assert.Equal(t, dir, plugin.Directory())
	assert.Equal(t, PluginTypeSkill, plugin.Type())
}

func TestNewRecipePluginAccessors(t *testing.T) {
	dir := filepath.Join("tmp", "recipes")

	plugin := NewRecipePlugin("workflows/deploy", "Deploy workflow", dir)

	assert.Equal(t, "workflows/deploy", plugin.Name())
	assert.Equal(t, "Deploy workflow", plugin.Description())
	assert.Equal(t, dir, plugin.Directory())
	assert.Equal(t, PluginTypeRecipe, plugin.Type())
}

func TestPluginConstructorsSatisfyPluginInterface(t *testing.T) {
	plugins := []Plugin{
		NewSkillPlugin("skill", "description", "skills/skill"),
		NewRecipePlugin("recipe", "description", "recipes"),
	}

	assert.Equal(t, PluginTypeSkill, plugins[0].Type())
	assert.Equal(t, PluginTypeRecipe, plugins[1].Type())
}
