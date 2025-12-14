package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"
)

const skillFileName = "SKILL.md"

// Discovery handles skill discovery from configured directories
type Discovery struct {
	skillDirs []string
}

// Option is a function that configures a Discovery
type Option func(*Discovery) error

// WithSkillDirs sets custom skill directories
func WithSkillDirs(dirs ...string) Option {
	return func(d *Discovery) error {
		d.skillDirs = dirs
		return nil
	}
}

// WithDefaultDirs initializes with default skill directories
func WithDefaultDirs() Option {
	return func(d *Discovery) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return errors.Wrap(err, "failed to get user home directory")
		}
		d.skillDirs = []string{
			"./.kodelet/skills",                          // Repo-local (higher precedence)
			filepath.Join(homeDir, ".kodelet", "skills"), // User-global
		}
		return nil
	}
}

// NewDiscovery creates a new skill discovery instance
func NewDiscovery(opts ...Option) (*Discovery, error) {
	d := &Discovery{}

	if len(opts) == 0 {
		if err := WithDefaultDirs()(d); err != nil {
			return nil, err
		}
	} else {
		for _, opt := range opts {
			if err := opt(d); err != nil {
				return nil, err
			}
		}
	}

	return d, nil
}

// DiscoverSkills finds all available skills from configured directories
func (d *Discovery) DiscoverSkills() (map[string]*Skill, error) {
	skills := make(map[string]*Skill)

	for _, dir := range d.skillDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Skip non-existent directories
			}
			return nil, errors.Wrapf(err, "failed to read skill directory %s", dir)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillPath := filepath.Join(dir, entry.Name(), skillFileName)
			skill, err := d.loadSkill(skillPath)
			if err != nil {
				continue // Skip invalid skills
			}

			// Only add if not already present (earlier directories have precedence)
			if _, exists := skills[skill.Name]; !exists {
				skill.Directory = filepath.Join(dir, entry.Name())
				skills[skill.Name] = skill
			}
		}
	}

	return skills, nil
}

// GetSkill returns a specific skill by name
func (d *Discovery) GetSkill(name string) (*Skill, error) {
	skills, err := d.DiscoverSkills()
	if err != nil {
		return nil, err
	}

	skill, exists := skills[name]
	if !exists {
		return nil, errors.Errorf("skill '%s' not found", name)
	}

	return skill, nil
}

// ListSkillNames returns the names of all available skills
func (d *Discovery) ListSkillNames() ([]string, error) {
	skills, err := d.DiscoverSkills()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}

	return names, nil
}

// loadSkill loads a single skill from its SKILL.md file
func (d *Discovery) loadSkill(path string) (*Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read skill file")
	}

	md := goldmark.New(
		goldmark.WithExtensions(meta.Meta),
	)

	var buf bytes.Buffer
	pctx := parser.NewContext()

	if err := md.Convert(content, &buf, parser.WithContext(pctx)); err != nil {
		return nil, errors.Wrap(err, "failed to parse markdown")
	}

	metaData := meta.Get(pctx)
	if metaData == nil {
		return nil, errors.New("missing frontmatter")
	}

	name, _ := metaData["name"].(string)
	description, _ := metaData["description"].(string)

	if name == "" {
		return nil, errors.New("skill name is required in frontmatter")
	}
	if description == "" {
		return nil, errors.New("skill description is required in frontmatter")
	}

	bodyContent := extractBodyContent(string(content))

	return &Skill{
		Name:        name,
		Description: description,
		Content:     bodyContent,
	}, nil
}

// extractBodyContent removes YAML frontmatter and returns the body
func extractBodyContent(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}

	lines := strings.Split(content, "\n")
	frontmatterEnd := -1

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			frontmatterEnd = i
			break
		}
	}

	if frontmatterEnd == -1 {
		return content
	}

	return strings.TrimLeft(strings.Join(lines[frontmatterEnd+1:], "\n"), "\n")
}

// FilterByAllowlist filters skills by an allowlist of names
// If the allowlist is empty, all skills are returned
func FilterByAllowlist(skills map[string]*Skill, allowed []string) map[string]*Skill {
	if len(allowed) == 0 {
		return skills
	}

	filtered := make(map[string]*Skill)
	for _, name := range allowed {
		if skill, exists := skills[name]; exists {
			filtered[name] = skill
		}
	}
	return filtered
}
