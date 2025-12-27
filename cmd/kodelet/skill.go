package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/skills"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type SkillAddConfig struct {
	Global bool
	Dir    string
}

func NewSkillAddConfig() *SkillAddConfig {
	return &SkillAddConfig{
		Global: false,
		Dir:    "",
	}
}

type SkillRemoveConfig struct {
	Global bool
}

func NewSkillRemoveConfig() *SkillRemoveConfig {
	return &SkillRemoveConfig{
		Global: false,
	}
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage kodelet skills",
	Long:  `Add, list, and remove kodelet skills from GitHub repositories.`,
	Run: func(cmd *cobra.Command, _ []string) {
		cmd.Help()
	},
}

var skillAddCmd = &cobra.Command{
	Use:   "add <repo>",
	Short: "Add skills from a GitHub repository",
	Long: `Add skills from a GitHub repository. The repository should contain directories
with SKILL.md files. You can specify:

  - A repo: orgname/skills (adds all skills)
  - A repo with specific skill: orgname/skills --dir skills/specific-skill
  - A repo with version: orgname/skills@v0.1.0 (adds from specific tag/branch/sha)

Examples:
  kodelet skill add orgname/skills
  kodelet skill add orgname/skills --dir skills/specific-skill
  kodelet skill add orgname/skills@main
  kodelet skill add orgname/skills -g`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config := getSkillAddConfigFromFlags(cmd)
		addSkillCmd(args[0], config)
	},
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed skills",
	Long:  `List all installed skills with their names, descriptions, and directory paths.`,
	Run: func(_ *cobra.Command, _ []string) {
		listSkillsCmd()
	},
}

var skillRemoveCmd = &cobra.Command{
	Use:   "remove <skill-name>",
	Short: "Remove an installed skill",
	Long: `Remove an installed skill by name.

Examples:
  kodelet skill remove specific-skill
  kodelet skill remove specific-skill -g`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		config := getSkillRemoveConfigFromFlags(cmd)
		removeSkillCmd(args[0], config)
	},
}

func init() {
	addDefaults := NewSkillAddConfig()
	skillAddCmd.Flags().BoolP("global", "g", addDefaults.Global, "Install to global ~/.kodelet/skills directory instead of local ./.kodelet/skills")
	skillAddCmd.Flags().StringP("dir", "d", addDefaults.Dir, "Path to a specific skill directory within the repository")

	removeDefaults := NewSkillRemoveConfig()
	skillRemoveCmd.Flags().BoolP("global", "g", removeDefaults.Global, "Remove from global ~/.kodelet/skills directory instead of local ./.kodelet/skills")

	skillCmd.AddCommand(skillAddCmd)
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillRemoveCmd)
	rootCmd.AddCommand(skillCmd)
}

func getSkillAddConfigFromFlags(cmd *cobra.Command) *SkillAddConfig {
	config := NewSkillAddConfig()
	if global, err := cmd.Flags().GetBool("global"); err == nil {
		config.Global = global
	}
	if dir, err := cmd.Flags().GetString("dir"); err == nil {
		config.Dir = dir
	}
	return config
}

func getSkillRemoveConfigFromFlags(cmd *cobra.Command) *SkillRemoveConfig {
	config := NewSkillRemoveConfig()
	if global, err := cmd.Flags().GetBool("global"); err == nil {
		config.Global = global
	}
	return config
}

func getSkillsDir(global bool) (string, error) {
	if global {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", errors.Wrap(err, "failed to get user home directory")
		}
		return filepath.Join(homeDir, ".kodelet", "skills"), nil
	}
	return ".kodelet/skills", nil
}

func addSkillCmd(repo string, config *SkillAddConfig) {
	if !isGhCliInstalled() {
		presenter.Error(errors.New("gh CLI is not installed"), "Please install the GitHub CLI (gh) to use this command")
		os.Exit(1)
	}

	if !isGhAuthenticated() {
		presenter.Error(errors.New("gh CLI is not authenticated"), "Please run 'gh auth login' to authenticate")
		os.Exit(1)
	}

	repoName, ref := parseRepoAndRef(repo)

	tmpDir, err := os.MkdirTemp("", "kodelet-skill-*")
	if err != nil {
		presenter.Error(err, "Failed to create temporary directory")
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	cloneArgs := []string{"repo", "clone", repoName, tmpDir}
	if ref != "" {
		cloneArgs = append(cloneArgs, "--", "--branch", ref, "--single-branch")
	}

	cmd := exec.Command("gh", cloneArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		presenter.Error(errors.Wrapf(err, "output: %s", string(output)), "Failed to clone repository")
		os.Exit(1)
	}

	skillsDir, err := getSkillsDir(config.Global)
	if err != nil {
		presenter.Error(err, "Failed to determine skills directory")
		os.Exit(1)
	}

	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		presenter.Error(err, "Failed to create skills directory")
		os.Exit(1)
	}

	var skillDirs []string
	if config.Dir != "" {
		targetPath := filepath.Join(tmpDir, config.Dir)
		skillFile := filepath.Join(targetPath, "SKILL.md")
		if _, err := os.Stat(skillFile); os.IsNotExist(err) {
			presenter.Error(errors.Errorf("no SKILL.md found at %s", config.Dir), "Invalid skill path")
			os.Exit(1)
		}
		skillDirs = []string{targetPath}
	} else {
		skillDirs, err = findSkillDirs(tmpDir)
		if err != nil {
			presenter.Error(err, "Failed to find skills in repository")
			os.Exit(1)
		}
	}

	if len(skillDirs) == 0 {
		presenter.Warning("No skills found in the repository")
		return
	}

	installed := 0
	for _, dir := range skillDirs {
		skillName := filepath.Base(dir)
		destDir := filepath.Join(skillsDir, skillName)

		if _, err := os.Stat(destDir); err == nil {
			presenter.Warning(fmt.Sprintf("Skill '%s' already exists, skipping", skillName))
			continue
		}

		if err := copyDir(dir, destDir); err != nil {
			presenter.Error(err, fmt.Sprintf("Failed to install skill '%s'", skillName))
			continue
		}

		installed++
		presenter.Success(fmt.Sprintf("Installed skill '%s' to %s", skillName, destDir))
	}

	if installed > 0 {
		presenter.Info(fmt.Sprintf("Successfully installed %d skill(s)", installed))
	}
}

func parseRepoAndRef(repo string) (string, string) {
	if idx := strings.LastIndex(repo, "@"); idx != -1 {
		return repo[:idx], repo[idx+1:]
	}
	return repo, ""
}

func findSkillDirs(root string) ([]string, error) {
	var skillDirs []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() && (info.Name() == ".git" || info.Name() == "node_modules") {
			return filepath.SkipDir
		}

		if !info.IsDir() && info.Name() == "SKILL.md" {
			skillDirs = append(skillDirs, filepath.Dir(path))
		}

		return nil
	})

	return skillDirs, err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		return copyFile(path, destPath)
	})
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func listSkillsCmd() {
	discovery, err := skills.NewDiscovery()
	if err != nil {
		presenter.Error(err, "Failed to initialize skill discovery")
		os.Exit(1)
	}

	allSkills, err := discovery.DiscoverSkills()
	if err != nil {
		presenter.Error(err, "Failed to discover skills")
		os.Exit(1)
	}

	if len(allSkills) == 0 {
		presenter.Info("No skills installed")
		return
	}

	names := make([]string, 0, len(allSkills))
	for name := range allSkills {
		names = append(names, name)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDIRECTORY\tDESCRIPTION")
	fmt.Fprintln(tw, "----\t---------\t-----------")

	for _, name := range names {
		skill := allSkills[name]
		description := skill.Description
		if len(description) > 60 {
			description = description[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", skill.Name, skill.Directory, description)
	}
	tw.Flush()
}

func removeSkillCmd(name string, config *SkillRemoveConfig) {
	skillsDir, err := getSkillsDir(config.Global)
	if err != nil {
		presenter.Error(err, "Failed to determine skills directory")
		os.Exit(1)
	}

	skillDir := filepath.Join(skillsDir, name)

	skillFile := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		location := "local"
		if config.Global {
			location = "global"
		}
		presenter.Error(errors.Errorf("skill '%s' not found in %s skills directory", name, location), "Skill not found")
		os.Exit(1)
	}

	if err := os.RemoveAll(skillDir); err != nil {
		presenter.Error(err, fmt.Sprintf("Failed to remove skill '%s'", name))
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Removed skill '%s' from %s", name, skillDir))
}
