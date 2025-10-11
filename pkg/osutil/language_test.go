package osutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectLanguageFromPath(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     string
	}{
		{"Go file", "main.go", "go"},
		{"Python file", "script.py", "python"},
		{"JavaScript file", "app.js", "javascript"},
		{"TypeScript file", "app.ts", "typescript"},
		{"TypeScript JSX", "component.tsx", "typescript"},
		{"JavaScript JSX", "component.jsx", "javascript"},
		{"Java file", "Main.java", "java"},
		{"C++ file", "main.cpp", "cpp"},
		{"C++ file with cc", "main.cc", "cpp"},
		{"C file", "main.c", "c"},
		{"Rust file", "main.rs", "rust"},
		{"Ruby file", "script.rb", "ruby"},
		{"PHP file", "index.php", "php"},
		{"Shell script", "script.sh", "bash"},
		{"Bash script", "script.bash", "bash"},
		{"YAML file", "config.yaml", "yaml"},
		{"YML file", "config.yml", "yaml"},
		{"JSON file", "data.json", "json"},
		{"XML file", "config.xml", "xml"},
		{"HTML file", "index.html", "html"},
		{"CSS file", "styles.css", "css"},
		{"Markdown file", "README.md", "markdown"},
		{"Text file", "notes.txt", "text"},
		{"SQL file", "schema.sql", "sql"},
		{"Unknown extension", "file.xyz", ""},
		{"No extension", "README", ""},
		{"Full path", "/path/to/file.go", "go"},
		{"Windows path", "C:\\Users\\test\\main.py", "python"},
		{"Case insensitive", "MAIN.GO", "go"},
		{"Multiple dots", "file.test.js", "javascript"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectLanguageFromPath(tt.filePath)
			assert.Equal(t, tt.want, got, "DetectLanguageFromPath(%q)", tt.filePath)
		})
	}
}
