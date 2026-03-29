package tools

// BashInput defines the input parameters for the bash tool.
type BashInput struct {
	Description string `json:"description" jsonschema:"description=A description of the command to run"`
	Command     string `json:"command" jsonschema:"description=The bash command to run"`
	Timeout     int    `json:"timeout" jsonschema:"description=Timeout in seconds (10-120)"`
}

// FileReadInput defines the input parameters for the file_read tool.
type FileReadInput struct {
	FilePath  string `json:"file_path" jsonschema:"description=The absolute path of the file to read"`
	Offset    int    `json:"offset" jsonschema:"description=The 1-indexed line number to start reading from. Default: 1"`
	LineLimit int    `json:"line_limit" jsonschema:"description=The maximum number of lines to read from the offset. Default: 2000. Max: 2000"`
}

// FileWriteInput defines the input parameters for the file_write tool.
type FileWriteInput struct {
	FilePath string `json:"file_path" jsonschema:"description=The absolute path of the file to write,required"`
	Text     string `json:"text" jsonschema:"description=The text of the file MUST BE provided"`
}

// FileEditInput defines the input parameters for the file_edit tool.
type FileEditInput struct {
	FilePath   string `json:"file_path" jsonschema:"description=The absolute path of the file to edit"`
	OldText    string `json:"old_text" jsonschema:"description=The text to be replaced"`
	NewText    string `json:"new_text" jsonschema:"description=The text to replace the old text with"`
	ReplaceAll bool   `json:"replace_all" jsonschema:"description=If true, replace all occurrences of old_text; if false (default), old_text must be unique"`
}

// ApplyPatchInput defines the JSON input for the apply_patch tool.
type ApplyPatchInput struct {
	Input string `json:"input" jsonschema:"description=The entire contents of the apply_patch command"`
}

// CodeSearchInput defines the input parameters for the grep_tool.
type CodeSearchInput struct {
	Pattern       string `json:"pattern" jsonschema:"description=The pattern to search for (regex by default or literal string if fixed_strings is true)"`
	Path          string `json:"path" jsonschema:"description=The absolute path to search in. Can be a directory (searches all files recursively) or a single file. Defaults to current working directory if not specified"`
	Include       string `json:"include" jsonschema:"description=The optional glob pattern to filter files for example: '*.go' '*.{go,py}'. Only applies when searching directories"`
	IgnoreCase    bool   `json:"ignore_case" jsonschema:"description=If true use case-insensitive search. Default is false (smart-case: case-insensitive if pattern is all lowercase)"`
	FixedStrings  bool   `json:"fixed_strings" jsonschema:"description=If true treat pattern as literal string instead of regex. Default is false"`
	SurroundLines int    `json:"surround_lines" jsonschema:"description=Number of lines to show before and after each match. Default is 0 (no context lines)"`
	MaxResults    int    `json:"max_results" jsonschema:"description=Maximum number of files to return results from. Default is 100. Use a smaller value to reduce output size"`
}

// GlobInput defines the input parameters for the glob_tool.
type GlobInput struct {
	Pattern         string `json:"pattern" jsonschema:"description=The glob pattern"`
	Path            string `json:"path" jsonschema:"description=The absolute path to a DIRECTORY to search in (not a file path). Defaults to current working directory if not specified"`
	IgnoreGitignore bool   `json:"ignore_gitignore,omitempty" jsonschema:"description=If true, do not respect .gitignore rules (default: false, meaning .gitignore is respected)"`
}

// ReadConversationInput defines the input parameters for the read_conversation tool.
type ReadConversationInput struct {
	ConversationID string `json:"conversation_id" jsonschema:"description=The ID of the saved conversation to read"`
	Goal           string `json:"goal" jsonschema:"description=What information to extract from the conversation"`
}
