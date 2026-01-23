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

func TestDiscoverSkillsWithSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	require.NoError(t, os.MkdirAll(skillsDir, 0o755))

	// Create actual skill directory outside the skills search path
	actualSkillDir := filepath.Join(tmpDir, "actual-skills", "symlinked-skill")
	require.NoError(t, os.MkdirAll(actualSkillDir, 0o755))
	skillContent := `---
name: symlinked-skill
description: A skill accessed via symlink
---

# Symlinked Skill

This skill is accessed through a symbolic link.
`
	require.NoError(t, os.WriteFile(filepath.Join(actualSkillDir, "SKILL.md"), []byte(skillContent), 0o644))

	// Create symlink in the skills directory pointing to actual skill
	symlinkPath := filepath.Join(skillsDir, "symlinked-skill")
	require.NoError(t, os.Symlink(actualSkillDir, symlinkPath))

	// Also create a regular skill to verify both work together
	regularSkillDir := filepath.Join(skillsDir, "regular-skill")
	require.NoError(t, os.MkdirAll(regularSkillDir, 0o755))
	regularContent := `---
name: regular-skill
description: A regular skill directory
---

# Regular Skill

This is a regular skill directory.
`
	require.NoError(t, os.WriteFile(filepath.Join(regularSkillDir, "SKILL.md"), []byte(regularContent), 0o644))

	discovery, err := NewDiscovery(WithSkillDirs(skillsDir))
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)
	assert.Len(t, skills, 2)

	// Verify symlinked skill is discovered
	symlinkedSkill, exists := skills["symlinked-skill"]
	require.True(t, exists, "symlinked skill should be discovered")
	assert.Equal(t, "symlinked-skill", symlinkedSkill.Name)
	assert.Equal(t, "A skill accessed via symlink", symlinkedSkill.Description)
	assert.Equal(t, symlinkPath, symlinkedSkill.Directory)
	assert.Contains(t, symlinkedSkill.Content, "accessed through a symbolic link")

	// Verify regular skill is still discovered
	regularSkill, exists := skills["regular-skill"]
	require.True(t, exists, "regular skill should be discovered")
	assert.Equal(t, "regular-skill", regularSkill.Name)
}

func TestDiscoverSkillsIgnoresSymlinkToFile(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	require.NoError(t, os.MkdirAll(skillsDir, 0o755))

	// Create a file to symlink to (not a directory)
	targetFile := filepath.Join(tmpDir, "somefile.txt")
	require.NoError(t, os.WriteFile(targetFile, []byte("just a file"), 0o644))

	// Create symlink to file in skills directory
	symlinkPath := filepath.Join(skillsDir, "file-symlink")
	require.NoError(t, os.Symlink(targetFile, symlinkPath))

	discovery, err := NewDiscovery(WithSkillDirs(skillsDir))
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)
	assert.Empty(t, skills, "symlink to file should be ignored")
}

func TestDiscoverSkillsIgnoresBrokenSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	require.NoError(t, os.MkdirAll(skillsDir, 0o755))

	// Create symlink to non-existent target
	symlinkPath := filepath.Join(skillsDir, "broken-symlink")
	require.NoError(t, os.Symlink("/non/existent/path", symlinkPath))

	discovery, err := NewDiscovery(WithSkillDirs(skillsDir))
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)
	assert.Empty(t, skills, "broken symlink should be ignored")
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
