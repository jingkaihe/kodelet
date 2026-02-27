package sysprompt

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

func RendererForConfig(llmConfig llm.Config) (*Renderer, error) {
	if strings.TrimSpace(llmConfig.Sysprompt) == "" {
		return defaultRenderer, nil
	}

	renderer, err := newRendererFromCustomTemplate(llmConfig.Sysprompt)
	if err != nil {
		return defaultRenderer, errors.Wrapf(err, "failed to load custom sysprompt %s", llmConfig.Sysprompt)
	}

	return renderer, nil
}

func newRendererFromCustomTemplate(templatePath string) (*Renderer, error) {
	resolvedPath, err := resolveCustomTemplatePath(templatePath)
	if err != nil {
		return nil, err
	}

	content, err := loadCustomTemplateContent(resolvedPath)
	if err != nil {
		return nil, err
	}

	overrides := map[string]string{
		SystemTemplate: content,
	}

	renderer := NewRendererWithTemplateOverride(TemplateFS, overrides)
	if renderer.parseErr != nil {
		return nil, errors.Wrapf(renderer.parseErr, "failed to parse custom sysprompt template %s", resolvedPath)
	}

	return renderer, nil
}

func resolveCustomTemplatePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("custom sysprompt path is empty")
	}

	if strings.ContainsRune(trimmed, '\x00') {
		return "", errors.New("sysprompt path contains null byte")
	}

	expanded, err := expandHomePath(trimmed)
	if err != nil {
		return "", err
	}

	absPath := expanded
	if !filepath.IsAbs(absPath) {
		absPath, err = filepath.Abs(absPath)
		if err != nil {
			return "", errors.Wrapf(err, "failed to resolve sysprompt path %s", trimmed)
		}
	}

	absPath = filepath.Clean(absPath)
	return absPath, nil
}

func validateCustomTemplateFile(resolvedPath string) (os.FileInfo, error) {
	fileInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to stat custom sysprompt template %s", resolvedPath)
	}

	if !fileInfo.Mode().IsRegular() {
		return nil, errors.Errorf("custom sysprompt template %s must be a regular file", resolvedPath)
	}

	return fileInfo, nil
}

func loadCustomTemplateContent(resolvedPath string) (string, error) {
	if _, err := validateCustomTemplateFile(resolvedPath); err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read custom sysprompt template %s", resolvedPath)
	}

	if !utf8.Valid(content) {
		return "", errors.Errorf("custom sysprompt template %s is not valid UTF-8", resolvedPath)
	}

	return string(content), nil
}

func expandHomePath(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve home directory for sysprompt path")
	}

	if path == "~" {
		return homeDir, nil
	}

	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[2:]), nil
	}

	return "", errors.Errorf("unsupported sysprompt path format: %s", path)
}
