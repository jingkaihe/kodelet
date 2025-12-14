package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDiscovery(t *testing.T) {
	t.Run("with default dirs", func(t *testing.T) {
		discovery, err := NewDiscovery()
		require.NoError(t, err)
		assert.NotNil(t, discovery)
		assert.Len(t, discovery.skillDirs, 2)
	})

	t.Run("with custom dirs", func(t *testing.T) {
		customDirs := []string{"/tmp/skills1", "/tmp/skills2"}
		discovery, err := NewDiscovery(WithSkillDirs(customDirs...))
		require.NoError(t, err)
		assert.Equal(t, customDirs, discovery.skillDirs)
	})
}

func TestDiscoverSkills(t *testing.T) {
	tmpDir := t.TempDir()

	skill1Dir := filepath.Join(tmpDir, "test-skill")
	require.NoError(t, os.MkdirAll(skill1Dir, 0o755))
	skill1Content := `---
name: test-skill
description: A test skill for unit testing
---

# Test Skill

## Instructions
This is a test skill.
`
	require.NoError(t, os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(skill1Content), 0o644))

	skill2Dir := filepath.Join(tmpDir, "another-skill")
	require.NoError(t, os.MkdirAll(skill2Dir, 0o755))
	skill2Content := `---
name: another-skill
description: Another test skill
---

# Another Skill

Some content here.
`
	require.NoError(t, os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(skill2Content), 0o644))

	discovery, err := NewDiscovery(WithSkillDirs(tmpDir))
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)
	assert.Len(t, skills, 2)

	testSkill, exists := skills["test-skill"]
	require.True(t, exists)
	assert.Equal(t, "test-skill", testSkill.Name)
	assert.Equal(t, "A test skill for unit testing", testSkill.Description)
	assert.Equal(t, skill1Dir, testSkill.Directory)
	assert.Contains(t, testSkill.Content, "# Test Skill")
	assert.Contains(t, testSkill.Content, "This is a test skill.")

	anotherSkill, exists := skills["another-skill"]
	require.True(t, exists)
	assert.Equal(t, "another-skill", anotherSkill.Name)
	assert.Equal(t, "Another test skill", anotherSkill.Description)
}

func TestDiscoveryPrecedence(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	skill1Dir := filepath.Join(tmpDir1, "shared-skill")
	require.NoError(t, os.MkdirAll(skill1Dir, 0o755))
	skill1Content := `---
name: shared-skill
description: From first directory
---

First directory content.
`
	require.NoError(t, os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(skill1Content), 0o644))

	skill2Dir := filepath.Join(tmpDir2, "shared-skill")
	require.NoError(t, os.MkdirAll(skill2Dir, 0o755))
	skill2Content := `---
name: shared-skill
description: From second directory
---

Second directory content.
`
	require.NoError(t, os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(skill2Content), 0o644))

	discovery, err := NewDiscovery(WithSkillDirs(tmpDir1, tmpDir2))
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)
	assert.Len(t, skills, 1)

	skill := skills["shared-skill"]
	assert.Equal(t, "From first directory", skill.Description)
	assert.Contains(t, skill.Content, "First directory content")
}

func TestSkillValidation(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("missing name", func(t *testing.T) {
		skillDir := filepath.Join(tmpDir, "no-name")
		require.NoError(t, os.MkdirAll(skillDir, 0o755))
		content := `---
description: Missing name field
---

Content here.
`
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

		discovery, err := NewDiscovery(WithSkillDirs(tmpDir))
		require.NoError(t, err)

		skills, err := discovery.DiscoverSkills()
		require.NoError(t, err)
		_, exists := skills["no-name"]
		assert.False(t, exists)
	})

	t.Run("missing description", func(t *testing.T) {
		skillDir := filepath.Join(tmpDir, "no-desc")
		require.NoError(t, os.MkdirAll(skillDir, 0o755))
		content := `---
name: no-desc
---

Content here.
`
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

		discovery, err := NewDiscovery(WithSkillDirs(tmpDir))
		require.NoError(t, err)

		skills, err := discovery.DiscoverSkills()
		require.NoError(t, err)
		_, exists := skills["no-desc"]
		assert.False(t, exists)
	})

	t.Run("no frontmatter", func(t *testing.T) {
		skillDir := filepath.Join(tmpDir, "no-frontmatter")
		require.NoError(t, os.MkdirAll(skillDir, 0o755))
		content := `# Just content
No frontmatter here.
`
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

		discovery, err := NewDiscovery(WithSkillDirs(tmpDir))
		require.NoError(t, err)

		skills, err := discovery.DiscoverSkills()
		require.NoError(t, err)
		_, exists := skills["no-frontmatter"]
		assert.False(t, exists)
	})
}

func TestExtractBodyContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "with frontmatter",
			input: `---
name: test
description: desc
---

# Content

Body text.`,
			expected: `# Content

Body text.`,
		},
		{
			name:     "no frontmatter",
			input:    "# Just content\nNo frontmatter.",
			expected: "# Just content\nNo frontmatter.",
		},
		{
			name: "incomplete frontmatter",
			input: `---
name: test
# No closing ---`,
			expected: `---
name: test
# No closing ---`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBodyContent(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterByAllowlist(t *testing.T) {
	skills := map[string]*Skill{
		"skill-a": {Name: "skill-a", Description: "A"},
		"skill-b": {Name: "skill-b", Description: "B"},
		"skill-c": {Name: "skill-c", Description: "C"},
	}

	t.Run("empty allowlist returns all", func(t *testing.T) {
		result := FilterByAllowlist(skills, nil)
		assert.Len(t, result, 3)
	})

	t.Run("allowlist filters skills", func(t *testing.T) {
		result := FilterByAllowlist(skills, []string{"skill-a", "skill-c"})
		assert.Len(t, result, 2)
		assert.Contains(t, result, "skill-a")
		assert.Contains(t, result, "skill-c")
		assert.NotContains(t, result, "skill-b")
	})

	t.Run("allowlist with unknown skill", func(t *testing.T) {
		result := FilterByAllowlist(skills, []string{"skill-a", "unknown"})
		assert.Len(t, result, 1)
		assert.Contains(t, result, "skill-a")
	})
}

func TestGetSkill(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := `---
name: test-skill
description: A test skill
---

Test content.
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

	discovery, err := NewDiscovery(WithSkillDirs(tmpDir))
	require.NoError(t, err)

	t.Run("existing skill", func(t *testing.T) {
		skill, err := discovery.GetSkill("test-skill")
		require.NoError(t, err)
		assert.Equal(t, "test-skill", skill.Name)
	})

	t.Run("non-existent skill", func(t *testing.T) {
		skill, err := discovery.GetSkill("unknown")
		assert.Error(t, err)
		assert.Nil(t, skill)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListSkillNames(t *testing.T) {
	tmpDir := t.TempDir()

	for _, name := range []string{"alpha", "beta", "gamma"} {
		skillDir := filepath.Join(tmpDir, name)
		require.NoError(t, os.MkdirAll(skillDir, 0o755))
		content := `---
name: ` + name + `
description: Skill ` + name + `
---

Content for ` + name + `.
`
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))
	}

	discovery, err := NewDiscovery(WithSkillDirs(tmpDir))
	require.NoError(t, err)

	names, err := discovery.ListSkillNames()
	require.NoError(t, err)
	assert.Len(t, names, 3)
	assert.Contains(t, names, "alpha")
	assert.Contains(t, names, "beta")
	assert.Contains(t, names, "gamma")
}

func TestNonExistentDirectory(t *testing.T) {
	discovery, err := NewDiscovery(WithSkillDirs("/non/existent/path"))
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)
	assert.Empty(t, skills)
}
