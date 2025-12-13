// Package skills provides an agentic skills system where the model can
// autonomously invoke specialized capabilities based on task context.
// Skills are packaged as directories containing a SKILL.md file with
// YAML frontmatter describing the skill's purpose and instructions.
package skills

// Skill represents a discovered skill with its metadata
type Skill struct {
	Name        string // Unique name from frontmatter
	Description string // Brief description for model decision-making
	Directory   string // Full path to the skill directory
	Content     string // Full content of SKILL.md (body, not frontmatter)
}

// Metadata represents the YAML frontmatter in SKILL.md files
type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}
