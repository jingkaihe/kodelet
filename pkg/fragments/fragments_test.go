package fragments

import (
	"context"
	"fmt"
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
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
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

	// Test with a command that produces output but fails
	fragmentContent := `Error output: {{bash "ls" "/nonexistent-directory-xyz"}}`
	fragmentPath := filepath.Join(tempDir, "failing.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "failing",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// Should contain the actual error output from the command, not a generic error message
	assert.Contains(t, result.Content, "Error output: ")
	// The output should contain the actual error message from ls command
	assert.Contains(t, result.Content, "cannot access")
	// Should NOT contain the old generic error format
	assert.NotContains(t, result.Content, "[ERROR executing command")
}

func TestFragmentProcessor_BashCommandNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test with a command that doesn't exist - this should return empty output
	fragmentContent := `Command not found: {{bash "nonexistent-command-xyz"}}`
	fragmentPath := filepath.Join(tempDir, "not-found.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "not-found",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// Since the command doesn't exist, there should be no output, just the prefix
	assert.Equal(t, "Command not found: ", result.Content)
}

func TestFragmentProcessor_BashCommandErrorWithOutput(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a test file first
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("hello world\ntest content"), 0o644)
	require.NoError(t, err)

	// Test with grep that produces no output but returns non-zero exit code
	fragmentContent := fmt.Sprintf(`Search result: {{bash "grep" "nonexistent" "%s"}}`, testFile)
	fragmentPath := filepath.Join(tempDir, "grep-test.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "grep-test",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// grep with no matches produces no output but returns exit code 1
	// So we should get just the prefix with no error message
	assert.Equal(t, "Search result: ", result.Content)
}

func TestFragmentProcessor_findFragmentFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.WriteFile(filepath.Join(tempDir, "test1.md"), []byte("test1"), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "test2"), []byte("test2"), 0o644)
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

	err = os.WriteFile(filepath.Join(highPrecDir, "same.md"), []byte("high priority"), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(lowPrecDir, "same.md"), []byte("low priority"), 0o644)
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
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
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

	err = os.WriteFile(filepath.Join(dir1, "with-meta.md"), []byte(fragmentWithMeta), 0o644)
	require.NoError(t, err)

	fragmentWithoutMeta := "Content without metadata"
	err = os.WriteFile(filepath.Join(dir1, "without-meta.md"), []byte(fragmentWithoutMeta), 0o644)
	require.NoError(t, err)

	fragmentUnique := `---
name: Unique Fragment
description: Only in second directory
---

Unique content`
	err = os.WriteFile(filepath.Join(dir2, "unique.md"), []byte(fragmentUnique), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(dir1, dir2))
	require.NoError(t, err)

	fragments, err := processor.ListFragmentsWithMetadata()
	require.NoError(t, err)

	assert.Len(t, fragments, 12) // 11 original + 1 compact recipe

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
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
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
	err = os.WriteFile(fragmentPath2, []byte(fragmentContent2), 0o644)
	require.NoError(t, err)

	metadata2, err := processor.GetFragmentMetadata("test-comma")
	require.NoError(t, err)

	assert.Equal(t, "Test Comma Format", metadata2.Metadata.Name)
	assert.Equal(t, []string{"bash", "file_read", "grep_tool"}, metadata2.Metadata.AllowedTools)
	assert.Equal(t, []string{"git *", "cat *"}, metadata2.Metadata.AllowedCommands)
}

func TestFragmentProcessor_Subdirectories(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-subdir-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create subdirectory structure
	githubDir := filepath.Join(tempDir, "github")
	err = os.MkdirAll(githubDir, 0o755)
	require.NoError(t, err)

	ciDir := filepath.Join(tempDir, "ci")
	err = os.MkdirAll(ciDir, 0o755)
	require.NoError(t, err)

	// Create fragments in subdirectories
	prFragmentContent := `---
name: GitHub PR Fragment
description: Fragment for GitHub PRs
---

Create a PR for {{.branch}} targeting {{.target}}.`

	issueFragmentContent := `---
name: GitHub Issue Fragment
description: Fragment for GitHub Issues
---

Create an issue about {{.topic}}.`

	ciFragmentContent := `---
name: CI Pipeline Fragment
description: Fragment for CI configuration
---

Setup CI for {{.language}} project.`

	// Write fragments to subdirectories
	err = os.WriteFile(filepath.Join(githubDir, "pr.md"), []byte(prFragmentContent), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(githubDir, "issue.md"), []byte(issueFragmentContent), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(ciDir, "setup.md"), []byte(ciFragmentContent), 0o644)
	require.NoError(t, err)

	// Also create a root-level fragment
	rootFragmentContent := `---
name: Root Fragment
description: Fragment in root directory
---

Root level fragment content.`

	err = os.WriteFile(filepath.Join(tempDir, "root.md"), []byte(rootFragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	// Test loading fragments with subdirectory paths
	prConfig := &Config{
		FragmentName: "github/pr",
		Arguments: map[string]string{
			"branch": "feature-branch",
			"target": "main",
		},
	}

	prResult, err := processor.LoadFragment(context.Background(), prConfig)
	require.NoError(t, err)
	assert.Equal(t, "GitHub PR Fragment", prResult.Metadata.Name)
	assert.Contains(t, prResult.Content, "Create a PR for feature-branch targeting main.")

	issueConfig := &Config{
		FragmentName: "github/issue",
		Arguments: map[string]string{
			"topic": "bug report",
		},
	}

	issueResult, err := processor.LoadFragment(context.Background(), issueConfig)
	require.NoError(t, err)
	assert.Equal(t, "GitHub Issue Fragment", issueResult.Metadata.Name)
	assert.Contains(t, issueResult.Content, "Create an issue about bug report.")

	ciConfig := &Config{
		FragmentName: "ci/setup",
		Arguments: map[string]string{
			"language": "Go",
		},
	}

	ciResult, err := processor.LoadFragment(context.Background(), ciConfig)
	require.NoError(t, err)
	assert.Equal(t, "CI Pipeline Fragment", ciResult.Metadata.Name)
	assert.Contains(t, ciResult.Content, "Setup CI for Go project.")

	// Test root level fragment still works
	rootConfig := &Config{
		FragmentName: "root",
		Arguments:    map[string]string{},
	}

	rootResult, err := processor.LoadFragment(context.Background(), rootConfig)
	require.NoError(t, err)
	assert.Equal(t, "Root Fragment", rootResult.Metadata.Name)
	assert.Contains(t, rootResult.Content, "Root level fragment content.")

	// Test listing fragments includes subdirectory fragments
	fragments, err := processor.ListFragmentsWithMetadata()
	require.NoError(t, err)

	fragmentNames := make(map[string]bool)
	for _, fragment := range fragments {
		// Extract fragment name from path for comparison
		path := fragment.Path
		if strings.HasPrefix(path, tempDir) {
			relPath, _ := filepath.Rel(tempDir, path)
			fragmentName := strings.TrimSuffix(relPath, ".md")
			fragmentName = filepath.ToSlash(fragmentName) // Normalize for comparison
			fragmentNames[fragmentName] = true
		}
	}

	assert.True(t, fragmentNames["github/pr"], "github/pr fragment should be found")
	assert.True(t, fragmentNames["github/issue"], "github/issue fragment should be found")
	assert.True(t, fragmentNames["ci/setup"], "ci/setup fragment should be found")
	assert.True(t, fragmentNames["root"], "root fragment should be found")
}

func TestFragmentProcessor_DefaultFunction(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-default-func-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test template default function
	fragmentContent := `Branch: {{default .branch "main"}}
Environment: {{default .env "development"}}
Provided: {{.custom_message}}
Optional: {{default .optional "N/A"}}`

	fragmentPath := filepath.Join(tempDir, "defaults.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	// Test 1: No arguments - should use inline defaults
	config := &Config{
		FragmentName: "defaults",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)
	assert.Contains(t, result.Content, "Branch: main")
	assert.Contains(t, result.Content, "Environment: development")
	assert.Contains(t, result.Content, "Provided: <no value>") // No default
	assert.Contains(t, result.Content, "Optional: N/A")

	// Test 2: Some arguments - should override inline defaults
	config2 := &Config{
		FragmentName: "defaults",
		Arguments: map[string]string{
			"branch":         "feature-x",
			"custom_message": "Custom message",
		},
	}

	result2, err := processor.LoadFragment(context.Background(), config2)
	require.NoError(t, err)
	assert.Contains(t, result2.Content, "Branch: feature-x")
	assert.Contains(t, result2.Content, "Environment: development") // Still uses default
	assert.Contains(t, result2.Content, "Provided: Custom message")
	assert.Contains(t, result2.Content, "Optional: N/A")
}

func TestFragmentProcessor_MetadataDefaults(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-yaml-defaults-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Recipe with metadata defaults
	fragmentContent := `---
name: Deployment Recipe
description: Deploy with sensible defaults
defaults:
  branch: main
  env: development
  target: production
---

Deploying branch: {{.branch}}
Environment: {{.env}}
Target: {{.target}}
Extra: {{.extra}}`

	fragmentPath := filepath.Join(tempDir, "deploy.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	// Test 1: No arguments - should use all metadata defaults
	config1 := &Config{
		FragmentName: "deploy",
		Arguments:    map[string]string{},
	}

	result1, err := processor.LoadFragment(context.Background(), config1)
	require.NoError(t, err)
	assert.Equal(t, "Deployment Recipe", result1.Metadata.Name)
	assert.Contains(t, result1.Content, "Deploying branch: main")
	assert.Contains(t, result1.Content, "Environment: development")
	assert.Contains(t, result1.Content, "Target: production")
	assert.Contains(t, result1.Content, "Extra: <no value>")

	// Test 2: Override some defaults
	config2 := &Config{
		FragmentName: "deploy",
		Arguments: map[string]string{
			"branch": "feature-123",
			"env":    "staging",
		},
	}

	result2, err := processor.LoadFragment(context.Background(), config2)
	require.NoError(t, err)
	assert.Contains(t, result2.Content, "Deploying branch: feature-123")
	assert.Contains(t, result2.Content, "Environment: staging")
	assert.Contains(t, result2.Content, "Target: production") // Still uses default

	// Test 3: Override all + add extra
	config3 := &Config{
		FragmentName: "deploy",
		Arguments: map[string]string{
			"branch": "hotfix",
			"env":    "production",
			"target": "eu-west-1",
			"extra":  "custom value",
		},
	}

	result3, err := processor.LoadFragment(context.Background(), config3)
	require.NoError(t, err)
	assert.Contains(t, result3.Content, "Deploying branch: hotfix")
	assert.Contains(t, result3.Content, "Environment: production")
	assert.Contains(t, result3.Content, "Target: eu-west-1")
	assert.Contains(t, result3.Content, "Extra: custom value")
}

func TestFragmentProcessor_HybridDefaults(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-hybrid-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Recipe combining YAML metadata defaults AND template default function
	fragmentContent := `---
name: Hybrid Recipe
description: Uses both YAML and template defaults
defaults:
  branch: main
  env: development
---

Branch: {{.branch}}
Environment: {{.env}}
Optional message: {{default .message "No message provided"}}
Optional tag: {{default .tag "latest"}}`

	fragmentPath := filepath.Join(tempDir, "hybrid.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "hybrid",
		Arguments: map[string]string{
			"message": "Custom deployment",
		},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// YAML metadata defaults applied
	assert.Contains(t, result.Content, "Branch: main")
	assert.Contains(t, result.Content, "Environment: development")

	// User arg overrides template default
	assert.Contains(t, result.Content, "Optional message: Custom deployment")

	// Template default used when no arg provided
	assert.Contains(t, result.Content, "Optional tag: latest")
}

func TestFragmentProcessor_EmptyDefaults(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-empty-defaults-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Recipe without defaults should work as before (backward compatibility)
	fragmentContent := `---
name: No Defaults Recipe
description: Recipe without any defaults
---

Branch: {{.branch}}
Environment: {{.env}}`

	fragmentPath := filepath.Join(tempDir, "no-defaults.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	// Without args, should render <no value> as before
	config := &Config{
		FragmentName: "no-defaults",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)
	assert.Contains(t, result.Content, "Branch: <no value>")
	assert.Contains(t, result.Content, "Environment: <no value>")
}

func TestFragmentProcessor_DefaultsWithConditionals(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-conditionals-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Recipe with defaults and conditional logic (like github/pr.md draft feature)
	fragmentContent := `---
name: PR Generator Test
description: Tests conditional rendering with defaults
defaults:
  draft: "false"
  target: "main"
---

Create a {{if eq .draft "true"}}**DRAFT** {{end}}pull request.

Target branch: {{.target}}
{{if eq .draft "true"}}
Draft PR instructions here.
{{else}}
Regular PR instructions here.
{{end}}`

	fragmentPath := filepath.Join(tempDir, "pr-test.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	// Test 1: Default (draft=false)
	config1 := &Config{
		FragmentName: "pr-test",
		Arguments:    map[string]string{},
	}

	result1, err := processor.LoadFragment(context.Background(), config1)
	require.NoError(t, err)
	assert.Contains(t, result1.Content, "Create a pull request.")
	assert.NotContains(t, result1.Content, "DRAFT")
	assert.Contains(t, result1.Content, "Target branch: main")
	assert.Contains(t, result1.Content, "Regular PR instructions here.")
	assert.NotContains(t, result1.Content, "Draft PR instructions here.")

	// Test 2: Override to draft=true
	config2 := &Config{
		FragmentName: "pr-test",
		Arguments: map[string]string{
			"draft": "true",
		},
	}

	result2, err := processor.LoadFragment(context.Background(), config2)
	require.NoError(t, err)
	assert.Contains(t, result2.Content, "Create a **DRAFT** pull request.")
	assert.Contains(t, result2.Content, "Target branch: main")
	assert.Contains(t, result2.Content, "Draft PR instructions here.")
	assert.NotContains(t, result2.Content, "Regular PR instructions here.")

	// Test 3: Override target while keeping default draft
	config3 := &Config{
		FragmentName: "pr-test",
		Arguments: map[string]string{
			"target": "develop",
		},
	}

	result3, err := processor.LoadFragment(context.Background(), config3)
	require.NoError(t, err)
	assert.Contains(t, result3.Content, "Target branch: develop")
	assert.NotContains(t, result3.Content, "DRAFT")
}

func TestFragmentProcessor_ParseHooksMetadata(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-hooks-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	fragmentContent := `---
name: Compaction Recipe
description: Recipe with turn_end hook
hooks:
  turn_end:
    handler: swap_context
    once: true
---

Summarize the conversation.`

	fragmentPath := filepath.Join(tempDir, "compact-test.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "compact-test",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	assert.Equal(t, "Compaction Recipe", result.Metadata.Name)
	assert.Equal(t, "Recipe with turn_end hook", result.Metadata.Description)
	require.NotNil(t, result.Metadata.Hooks)
	require.Contains(t, result.Metadata.Hooks, "turn_end")

	hookConfig := result.Metadata.Hooks["turn_end"]
	assert.Equal(t, "swap_context", hookConfig.Handler)
	assert.True(t, hookConfig.Once)
}

func TestFragmentProcessor_ParseHooksMetadata_MultipleHooks(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-multi-hooks-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	fragmentContent := `---
name: Multi-hook Recipe
description: Recipe with multiple hooks
hooks:
  turn_end:
    handler: swap_context
    once: false
  before_tool_call:
    handler: audit_logger
---

Content here.`

	fragmentPath := filepath.Join(tempDir, "multi-hook.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "multi-hook",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	require.Len(t, result.Metadata.Hooks, 2)

	turnEndHook := result.Metadata.Hooks["turn_end"]
	assert.Equal(t, "swap_context", turnEndHook.Handler)
	assert.False(t, turnEndHook.Once)

	beforeToolCallHook := result.Metadata.Hooks["before_tool_call"]
	assert.Equal(t, "audit_logger", beforeToolCallHook.Handler)
}

func TestFragmentProcessor_ParseHooksMetadata_NoHooks(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-no-hooks-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	fragmentContent := `---
name: Simple Recipe
description: Recipe without hooks
---

Content here.`

	fragmentPath := filepath.Join(tempDir, "simple.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "simple",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	assert.Nil(t, result.Metadata.Hooks)
}

func TestLoadCompactPrompt(t *testing.T) {
	ctx := context.Background()

	prompt, err := LoadCompactPrompt(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)

	// Verify the prompt contains key sections from compact.md
	assert.Contains(t, prompt, "summary")
	assert.Contains(t, prompt, "Objective")
	assert.Contains(t, prompt, "Explicit Request and Intention")
	assert.Contains(t, prompt, "Key Technical Concepts")
	assert.Contains(t, prompt, "Files and Code Snippets Examined")
	assert.Contains(t, prompt, "Errors and Fixes Applied")
	assert.Contains(t, prompt, "Pending Tasks")
	assert.Contains(t, prompt, "Current Work in Progress")
}

func TestFragmentProcessor_ParseHooksMetadata_EmptyHandler(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-empty-handler-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	fragmentContent := `---
name: Empty Handler Recipe
description: Recipe with empty handler name
hooks:
  turn_end:
    handler: ""
    once: true
---

Content here.`

	fragmentPath := filepath.Join(tempDir, "empty-handler.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "empty-handler",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	require.Contains(t, result.Metadata.Hooks, "turn_end")
	hookConfig := result.Metadata.Hooks["turn_end"]
	assert.Equal(t, "", hookConfig.Handler)
	assert.True(t, hookConfig.Once)
}

func TestFragmentProcessor_ParseHooksMetadata_OnlyHandlerField(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-only-handler-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test with only handler field specified (once defaults to false)
	fragmentContent := `---
name: Handler Only Recipe
description: Recipe with only handler specified
hooks:
  turn_end:
    handler: swap_context
---

Content here.`

	fragmentPath := filepath.Join(tempDir, "handler-only.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "handler-only",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	require.Contains(t, result.Metadata.Hooks, "turn_end")
	hookConfig := result.Metadata.Hooks["turn_end"]
	assert.Equal(t, "swap_context", hookConfig.Handler)
	assert.False(t, hookConfig.Once) // Default value
}

func TestFragmentProcessor_ParseHooksMetadata_InvalidHookType(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-invalid-hook-type-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test with a hook type that doesn't exist in the system
	fragmentContent := `---
name: Invalid Hook Type Recipe
description: Recipe with invalid hook type
hooks:
  nonexistent_hook:
    handler: swap_context
    once: true
---

Content here.`

	fragmentPath := filepath.Join(tempDir, "invalid-hook-type.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "invalid-hook-type",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// The parser should still parse it - validation happens at execution time
	require.Contains(t, result.Metadata.Hooks, "nonexistent_hook")
	hookConfig := result.Metadata.Hooks["nonexistent_hook"]
	assert.Equal(t, "swap_context", hookConfig.Handler)
	assert.True(t, hookConfig.Once)
}

func TestFragmentProcessor_ParseHooksMetadata_EmptyHooksMap(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kodelet-fragments-empty-hooks-map-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test with explicit empty hooks map
	fragmentContent := `---
name: Empty Hooks Map Recipe
description: Recipe with empty hooks map
hooks: {}
---

Content here.`

	fragmentPath := filepath.Join(tempDir, "empty-hooks-map.md")
	err = os.WriteFile(fragmentPath, []byte(fragmentContent), 0o644)
	require.NoError(t, err)

	processor, err := NewFragmentProcessor(WithFragmentDirs(tempDir))
	require.NoError(t, err)

	config := &Config{
		FragmentName: "empty-hooks-map",
		Arguments:    map[string]string{},
	}

	result, err := processor.LoadFragment(context.Background(), config)
	require.NoError(t, err)

	// Should have empty hooks map, not nil
	assert.NotNil(t, result.Metadata.Hooks)
	assert.Len(t, result.Metadata.Hooks, 0)
}
