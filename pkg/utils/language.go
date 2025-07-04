package utils

import (
	"path/filepath"
	"strings"
)

// Common language mappings for file extensions
var extensionToLanguage = map[string]string{
	"go":      "go",
	"py":      "python",
	"js":      "javascript",
	"ts":      "typescript",
	"tsx":     "typescript",
	"jsx":     "javascript",
	"java":    "java",
	"cpp":     "cpp",
	"cc":      "cpp",
	"cxx":     "cpp",
	"c":       "c",
	"h":       "c",
	"hpp":     "cpp",
	"rs":      "rust",
	"rb":      "ruby",
	"php":     "php",
	"sh":      "bash",
	"bash":    "bash",
	"zsh":     "shell",
	"fish":    "shell",
	"yaml":    "yaml",
	"yml":     "yaml",
	"json":    "json",
	"xml":     "xml",
	"html":    "html",
	"htm":     "html",
	"css":     "css",
	"scss":    "scss",
	"sass":    "sass",
	"less":    "less",
	"md":      "markdown",
	"txt":     "text",
	"sql":     "sql",
	"graphql": "graphql",
	"gql":     "graphql",
	"vim":     "vim",
	"lua":     "lua",
	"r":       "r",
	"swift":   "swift",
	"kt":      "kotlin",
	"scala":   "scala",
	"clj":     "clojure",
	"ex":      "elixir",
	"exs":     "elixir",
	"erl":     "erlang",
	"hrl":     "erlang",
	"ml":      "ocaml",
	"mli":     "ocaml",
	"fs":      "fsharp",
	"fsi":     "fsharp",
	"fsx":     "fsharp",
	"pl":      "perl",
	"pm":      "perl",
	"dart":    "dart",
	"vue":     "vue",
	"svelte":  "svelte",
}

// DetectLanguageFromPath detects the programming language from a file path based on its extension.
// Returns an empty string if the language cannot be determined.
func DetectLanguageFromPath(filePath string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))
	if ext == "" {
		return ""
	}

	if lang, ok := extensionToLanguage[ext]; ok {
		return lang
	}

	return ""
}
