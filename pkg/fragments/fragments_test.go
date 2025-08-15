package fragments

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFragmentProcessor_LoadFragment(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	fragmentContent := `---
name: Test Fragment
description: A test fragment
---

Hello {{.name}}!
Your role is {{.role}}.
Current date: {{bash "date" "+%Y-%m-%d"}}
Command result: {{bash "echo" "test"}}`

	fragmentPath := filepath.Join(tempDir, "test.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "test",
		Arguments: map[string]string{
			"name": "Alice",
			"role": "Engineer",
		},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	assert.Contains(t, result.Content, "Hello Alice!")
	assert.Contains(t, result.Content, "Your role is Engineer.")
	assert.Contains(t, result.Content, "Current date: 20")
	assert.Contains(t, result.Content, "Command result: test")
}

func TestFragmentProcessor_BashCommandError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	fragmentContent := `This will fail: {{bash "nonexistent-command-xyz"}}`
	fragmentPath := filepath.Join(tempDir, "failing.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "failing",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	assert.Contains(t, result.Content, "[ERROR executing command")
	assert.Contains(t, result.Content, "nonexistent-command-xyz")
}

func TestFragmentProcessor_findFragmentFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.WriteFile(filepath.Join(tempDir, "test1.md"), []byte("test1"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "test2"), []byte("test2"), 0644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	path, err := processor.findFragmentFile("test1")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "test1.md"), path)

	path, err = processor.findFragmentFile("test2")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "test2"), path)

	_, err = processor.findFragmentFile("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fragment 'nonexistent' not found")
}

func TestFragmentProcessor_DirectoryPrecedence(t *testing.T) {
	highPrecDir, err := os.MkdirTemp("", "kodelet-fragments-high")
	require.NoError(t, err)
	defer os.RemoveAll(highPrecDir)

	lowPrecDir, err := os.MkdirTemp("", "kodelet-fragments-low")
	require.NoError(t, err)
	defer os.RemoveAll(lowPrecDir)

	err = os.WriteFile(filepath.Join(highPrecDir, "same.md"), []byte("high priority"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(lowPrecDir, "same.md"), []byte("low priority"), 0644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(highPrecDir, lowPrecDir))
	require.NoError(t, err)

	path, err := processor.findFragmentFile("same")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(highPrecDir, "same.md"), path)
}

func TestFragmentProcessor_processTemplate(t *testing.T) {
	processor, err := NewFragmentProcessor()
	require.NoError(t, err)

	content := `Hello {{.name}}! You work as a {{.job}}.
Today is {{bash "date" "+%A"}}.
Echo test: {{bash "echo" "hello world"}}`

	args := map[string]string{
		"name": "Bob",
		"job":  "Developer",
	}

	result, err := processor.processTemplate(context.Background(), content, args)
	require.NoError(t, err)

	assert.Contains(t, result, "Hello Bob! You work as a Developer.")
	assert.Contains(t, result, "Today is ")
	assert.Contains(t, result, "Echo test: hello world")
}

func TestFragmentProcessor_ErrorHandling(t *testing.T) {
	processor, err := NewFragmentProcessor()
	require.NoError(t, err)

	content := "Hello {{range}}"
	_, err = processor.processTemplate(context.Background(), content, map[string]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse template")

	content = "Hello {{.missing}}"
	result, err := processor.processTemplate(context.Background(), content, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "Hello <no value>", result)

	config := &Config{
		FragmentName: "nonexistent-fragment-xyz",
		Arguments:    map[string]string{},
	}
	_, err = processor.LoadFragment(context.Background(), config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fragment 'nonexistent-fragment-xyz' not found")
}

func TestFragmentProcessor_BuilderPattern(t *testing.T) {
	processor, err := NewFragmentProcessor()
	require.NoError(t, err)
	assert.Len(t, processor.fragmentDirs, 2)
	assert.Equal(t, "./recipes", processor.fragmentDirs[0])
	assert.True(t, strings.HasSuffix(processor.fragmentDirs[1], "/.kodelet/recipes"))

	processor, err = NewFragmentProcessor(WithFragmentDirs("/custom1", "/custom2"))
	require.NoError(t, err)
	assert.Len(t, processor.fragmentDirs, 2)
	assert.Equal(t, "/custom1", processor.fragmentDirs[0])
	assert.Equal(t, "/custom2", processor.fragmentDirs[1])

	processor, err = NewFragmentProcessor(WithAdditionalDirs("/extra1"))
	require.NoError(t, err)
	assert.Len(t, processor.fragmentDirs, 3)
	assert.Equal(t, "./recipes", processor.fragmentDirs[0])
	assert.True(t, strings.HasSuffix(processor.fragmentDirs[1], "/.kodelet/recipes"))
	assert.Equal(t, "/extra1", processor.fragmentDirs[2])

	_, err = NewFragmentProcessor(WithFragmentDirs())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one fragment directory must be specified")
}

func TestFragmentProcessor_parseFrontmatter(t *testing.T) {
	processor, err := NewFragmentProcessor()
	require.NoError(t, err)

	contentWithFrontmatter := `---
name: Test Recipe
description: A test recipe for testing
---

Hello {{.name}}!`

	metadata, content, err := processor.parseFrontmatter(contentWithFrontmatter)
	require.NoError(t, err)
	assert.Equal(t, "Test Recipe", metadata.Name)
	assert.Equal(t, "A test recipe for testing", metadata.Description)
	assert.Equal(t, "\nHello {{.name}}!", content)

	contentWithoutFrontmatter := "Hello {{.name}}!"
	metadata, content, err = processor.parseFrontmatter(contentWithoutFrontmatter)
	require.NoError(t, err)
	assert.Equal(t, "", metadata.Name)
	assert.Equal(t, "", metadata.Description)
	assert.Equal(t, "Hello {{.name}}!", content)

	contentInvalidYAML := `---
name: [invalid yaml
description: test
---

Hello {{.name}}!`
	metadata, content, err = processor.parseFrontmatter(contentInvalidYAML)
	require.NoError(t, err)
	assert.Equal(t, "", metadata.Name)
	assert.Equal(t, "", metadata.Description)
	assert.Equal(t, "\nHello {{.name}}!", content)
}

func TestFragmentProcessor_LoadFragmentMetadata(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-metadata-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	fragmentContent := `---
name: Test Fragment
description: A test fragment with metadata
---

Hello {{.name}}!
Your role is {{.role}}.
Current date: {{bash "date" "+%Y-%m-%d"}}`

	fragmentPath := filepath.Join(tempDir, "test-with-metadata.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "test-with-metadata",
		Arguments: map[string]string{
			"name": "Alice",
			"role": "Engineer",
		},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	assert.Equal(t, "Test Fragment", result.Metadata.Name)
	assert.Equal(t, "A test fragment with metadata", result.Metadata.Description)
	assert.Equal(t, fragmentPath, result.Path)

	assert.Contains(t, result.Content, "Hello Alice!")
	assert.Contains(t, result.Content, "Your role is Engineer.")
	assert.Contains(t, result.Content, "Current date: 20")
}

func TestFragmentProcessor_ListFragmentsWithMetadata(t *testing.T) {
	dir1, err := os.MkdirTemp("", "kodelet-fragments-meta-1")
	require.NoError(t, err)
	defer os.RemoveAll(dir1)

	dir2, err := os.MkdirTemp("", "kodelet-fragments-meta-2")
	require.NoError(t, err)
	defer os.RemoveAll(dir2)

	fragmentWithMeta := `---
name: Fragment With Meta
description: This fragment has metadata
---

Content here`

	err = os.WriteFile(filepath.Join(dir1, "with-meta.md"), []byte(fragmentWithMeta), 0644)
	require.NoError(t, err)

	fragmentWithoutMeta := "Content without metadata"
	err = os.WriteFile(filepath.Join(dir1, "without-meta.md"), []byte(fragmentWithoutMeta), 0644)
	require.NoError(t, err)

	fragmentUnique := `---
name: Unique Fragment
description: Only in second directory
---

Unique content`
	err = os.WriteFile(filepath.Join(dir2, "unique.md"), []byte(fragmentUnique), 0644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(dir1, dir2))
	require.NoError(t, err)

	fragments, err := processor.ListFragmentsWithMetadata()
	require.NoError(t, err)

	// Should include 3 filesystem fragments + 2 built-in recipes (issue-resolve and commit)
	assert.Len(t, fragments, 5)

	var withMeta, withoutMeta, unique *Fragment
	for _, f := range fragments {
		switch filepath.Base(f.Path) {
		case "with-meta.md":
			withMeta = f
		case "without-meta.md":
			withoutMeta = f
		case "unique.md":
			unique = f
		}
	}

	require.NotNil(t, withMeta)
	assert.Equal(t, "Fragment With Meta", withMeta.Metadata.Name)
	assert.Equal(t, "This fragment has metadata", withMeta.Metadata.Description)
	assert.Contains(t, withMeta.Path, dir1)

	require.NotNil(t, withoutMeta)
	assert.Equal(t, "without-meta", withoutMeta.Metadata.Name)
	assert.Equal(t, "", withoutMeta.Metadata.Description)

	require.NotNil(t, unique)
	assert.Equal(t, "Unique Fragment", unique.Metadata.Name)
	assert.Equal(t, "Only in second directory", unique.Metadata.Description)
	assert.Contains(t, unique.Path, dir2)
}

func TestFragmentProcessor_ParseAllowedToolsAndCommands(t *testing.T) {
	dir, err := os.MkdirTemp("", "fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Test fragment with both YAML array and comma-separated formats
	fragmentContent := `---
name: Test Restrictions
description: Fragment with tool and command restrictions
allowed_tools:
  - "bash"
  - "file_read"
  - "thinking"
allowed_commands: "ls *,echo *,pwd"
---

Test content here.`

	fragmentPath := filepath.Join(dir, "test-restrictions.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(dir))
	require.NoError(t, err)

	metadata, err := processor.GetFragmentMetadata("test-restrictions")
	require.NoError(t, err)

	assert.Equal(t, "Test Restrictions", metadata.Metadata.Name)
	assert.Equal(t, "Fragment with tool and command restrictions", metadata.Metadata.Description)
	assert.Equal(t, []string{"bash", "file_read", "thinking"}, metadata.Metadata.AllowedTools)
	assert.Equal(t, []string{"ls *", "echo *", "pwd"}, metadata.Metadata.AllowedCommands)

	// Test fragment with comma-separated tools
	fragmentContent2 := `---
name: Test Comma Format
description: Fragment with comma-separated tools
allowed_tools: "bash,file_read,grep_tool"
allowed_commands:
  - "git *"
  - "cat *"
---

Test content here.`

	fragmentPath2 := filepath.Join(dir, "test-comma.md")
	err = os.WriteFile(fragmentPath2, []byte(fragmentContent2), 0644)
	require.NoError(t, err)

	metadata2, err := processor.GetFragmentMetadata("test-comma")
	require.NoError(t, err)

	assert.Equal(t, "Test Comma Format", metadata2.Metadata.Name)
	assert.Equal(t, []string{"bash", "file_read", "grep_tool"}, metadata2.Metadata.AllowedTools)
	assert.Equal(t, []string{"git *", "cat *"}, metadata2.Metadata.AllowedCommands)
}
