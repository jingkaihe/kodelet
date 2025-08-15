package fragments

import (
	_ "embed"
	"io/fs"
	"path"
	"strings"
	"time"
)

// Embedded builtin fragment files
var (
	//go:embed recipes/issue-resolve.md
	issueResolveContent string
	
	//go:embed recipes/commit-message.md
	commitMessageContent string
	
	//go:embed recipes/pr-response.md
	prResponseContent string
	
	//go:embed recipes/pr-generation.md
	prGenerationContent string
)

// BuiltinFS implements fs.FS interface for builtin fragments
type BuiltinFS struct{}

// Open implements fs.FS interface
func (b BuiltinFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &builtinDir{}, nil
	}
	
	content, ok := getBuiltinContent(name)
	if !ok {
		return nil, fs.ErrNotExist
	}
	
	return &builtinFile{
		name:    path.Base(name),
		content: content,
	}, nil
}

// ReadDir returns directory entries for builtin fragments
func (b BuiltinFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." && name != "" {
		return nil, fs.ErrNotExist
	}
	
	entries := []fs.DirEntry{
		&builtinDirEntry{name: "issue-resolve.md"},
		&builtinDirEntry{name: "commit-message.md"},
		&builtinDirEntry{name: "pr-response.md"},
		&builtinDirEntry{name: "pr-generation.md"},
	}
	
	return entries, nil
}

// getBuiltinContent returns the content for a builtin fragment
func getBuiltinContent(name string) (string, bool) {
	// Normalize the name (remove leading slash, .md extension variations)
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "./")
	
	switch name {
	case "issue-resolve.md", "issue-resolve":
		return issueResolveContent, true
	case "commit-message.md", "commit-message":
		return commitMessageContent, true
	case "pr-response.md", "pr-response":
		return prResponseContent, true
	case "pr-generation.md", "pr-generation":
		return prGenerationContent, true
	default:
		return "", false
	}
}

// builtinFile implements fs.File for builtin fragment content
type builtinFile struct {
	name    string
	content string
	reader  *strings.Reader
}

func (f *builtinFile) Stat() (fs.FileInfo, error) {
	return &builtinFileInfo{
		name: f.name,
		size: int64(len(f.content)),
	}, nil
}

func (f *builtinFile) Read(b []byte) (int, error) {
	if f.reader == nil {
		f.reader = strings.NewReader(f.content)
	}
	return f.reader.Read(b)
}

func (f *builtinFile) Close() error {
	return nil
}

// builtinDir implements fs.File for the builtin directory
type builtinDir struct{}

func (d *builtinDir) Stat() (fs.FileInfo, error) {
	return &builtinFileInfo{
		name:  ".",
		isDir: true,
	}, nil
}

func (d *builtinDir) Read([]byte) (int, error) {
	return 0, fs.ErrInvalid
}

func (d *builtinDir) Close() error {
	return nil
}

// builtinFileInfo implements fs.FileInfo
type builtinFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *builtinFileInfo) Name() string       { return fi.name }
func (fi *builtinFileInfo) Size() int64        { return fi.size }
func (fi *builtinFileInfo) Mode() fs.FileMode  { 
	if fi.isDir {
		return fs.ModeDir | 0755
	}
	return 0644
}
func (fi *builtinFileInfo) ModTime() time.Time    { return time.Time{} }
func (fi *builtinFileInfo) IsDir() bool        { return fi.isDir }
func (fi *builtinFileInfo) Sys() interface{}   { return nil }

// builtinDirEntry implements fs.DirEntry
type builtinDirEntry struct {
	name string
}

func (e *builtinDirEntry) Name() string               { return e.name }
func (e *builtinDirEntry) IsDir() bool                { return false }
func (e *builtinDirEntry) Type() fs.FileMode          { return 0644 }
func (e *builtinDirEntry) Info() (fs.FileInfo, error) {
	return &builtinFileInfo{
		name: e.name,
		size: 0, // Size will be determined when file is opened
	}, nil
}

// NewBuiltinFS creates a new builtin filesystem
func NewBuiltinFS() fs.FS {
	return &BuiltinFS{}
}