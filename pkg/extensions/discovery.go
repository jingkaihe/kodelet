package extensions

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/plugins"
	"github.com/pkg/errors"
)

const extensionExecutablePrefix = "kodelet-extension-"

// SourceKind identifies where an extension was discovered.
type SourceKind string

const (
	SourceKindLocalStandalone  SourceKind = "local_standalone"
	SourceKindLocalPlugin      SourceKind = "local_plugin"
	SourceKindGlobalStandalone SourceKind = "global_standalone"
	SourceKindGlobalPlugin     SourceKind = "global_plugin"
)

// Root describes a directory that should be scanned for extension executables.
type Root struct {
	Dir          string
	Kind         SourceKind
	PluginPrefix string
}

// Extension describes a discovered extension executable.
type Extension struct {
	ID           string
	Name         string
	ExecPath     string
	Dir          string
	RootDir      string
	Kind         SourceKind
	PluginPrefix string
	PluginRef    string
}

// Discovery handles extension discovery.
type Discovery struct {
	config     Config
	roots      []Root
	workingDir string
}

// DiscoveryOption configures extension discovery.
type DiscoveryOption func(*Discovery) error

// WithConfig sets discovery config.
func WithConfig(config Config) DiscoveryOption {
	return func(d *Discovery) error {
		d.config = config
		return nil
	}
}

// WithRoots replaces discovery roots. Intended for tests.
func WithRoots(roots ...Root) DiscoveryOption {
	return func(d *Discovery) error {
		d.roots = roots
		return nil
	}
}

// WithWorkingDir sets the working directory for relative path normalization.
func WithWorkingDir(workingDir string) DiscoveryOption {
	return func(d *Discovery) error {
		workingDir = strings.TrimSpace(workingDir)
		if workingDir == "" {
			return nil
		}
		d.workingDir = workingDir
		return nil
	}
}

// NewDiscovery creates an extension discovery instance.
func NewDiscovery(opts ...DiscoveryOption) (*Discovery, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get working directory")
	}
	d := &Discovery{
		config:     DefaultConfig(),
		workingDir: wd,
	}
	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	if d.roots == nil {
		roots, err := d.defaultRoots()
		if err != nil {
			return nil, err
		}
		d.roots = roots
	}
	return d, nil
}

func (d *Discovery) defaultRoots() ([]Root, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user home directory")
	}

	roots := []Root{
		{Dir: d.config.LocalDir, Kind: SourceKindLocalStandalone},
	}
	roots = append(roots, pluginExtensionRoots(filepath.Join(".kodelet", "plugins"), SourceKindLocalPlugin)...)
	roots = append(roots, Root{Dir: d.config.GlobalDir, Kind: SourceKindGlobalStandalone})
	roots = append(roots, pluginExtensionRoots(filepath.Join(homeDir, ".kodelet", "plugins"), SourceKindGlobalPlugin)...)
	return roots, nil
}

func pluginExtensionRoots(pluginsDir string, kind SourceKind) []Root {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil
	}
	roots := make([]Root, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(pluginsDir, entry.Name(), "extensions")
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, Root{
				Dir:          root,
				Kind:         kind,
				PluginPrefix: entry.Name(),
			})
		}
	}
	return roots
}

// Discover finds extension executables in configured roots.
func (d *Discovery) Discover() ([]Extension, error) {
	if !d.config.Enabled {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var discovered []Extension

	allowMatcher := newMatcher(d.config.Allow, d.workingDir)
	denyMatcher := newMatcher(d.config.Deny, d.workingDir)

	for _, root := range d.roots {
		extensions, err := d.discoverRoot(root)
		if err != nil {
			if os.IsNotExist(errors.Cause(err)) {
				continue
			}
			return nil, err
		}
		for _, ext := range extensions {
			if _, ok := seen[ext.ID]; ok {
				continue
			}
			if denyMatcher.matches(ext) || !allowMatcher.allows(ext) {
				continue
			}
			seen[ext.ID] = struct{}{}
			discovered = append(discovered, ext)
		}
	}

	return discovered, nil
}

func (d *Discovery) discoverRoot(root Root) ([]Extension, error) {
	rootDir := normalizePath(root.Dir, d.workingDir)
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read extension root %s", rootDir)
	}

	var extensions []Extension
	for _, entry := range entries {
		entryPath := filepath.Join(rootDir, entry.Name())
		if entry.IsDir() {
			nested, err := discoverNestedExtension(root, rootDir, entry.Name(), entryPath)
			if err == nil {
				extensions = append(extensions, nested)
			}
			continue
		}

		ext, err := discoverDirectExtension(root, rootDir, entry.Name(), entryPath)
		if err == nil {
			extensions = append(extensions, ext)
		}
	}
	sort.SliceStable(extensions, func(i, j int) bool { return extensions[i].ID < extensions[j].ID })
	return extensions, nil
}

func discoverDirectExtension(root Root, rootDir, name, path string) (Extension, error) {
	if !strings.HasPrefix(name, extensionExecutablePrefix) {
		return Extension{}, errors.New("not an extension executable")
	}
	if !isExecutable(path) {
		return Extension{}, errors.New("not executable")
	}
	extName := strings.TrimPrefix(name, extensionExecutablePrefix)
	return buildExtension(root, rootDir, rootDir, path, extName), nil
}

func discoverNestedExtension(root Root, rootDir, dirName, dirPath string) (Extension, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return Extension{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), extensionExecutablePrefix) {
			continue
		}
		path := filepath.Join(dirPath, entry.Name())
		if isExecutable(path) {
			return buildExtension(root, rootDir, dirPath, path, dirName), nil
		}
	}
	return Extension{}, errors.New("no extension executable")
}

func buildExtension(root Root, rootDir, extDir, execPath, name string) Extension {
	pluginRef := ""
	id := name
	if root.PluginPrefix != "" {
		pluginRef = root.PluginPrefix + "/" + name
		id = pluginRef
	}
	return Extension{
		ID:           id,
		Name:         name,
		ExecPath:     osutil.CanonicalizePath(execPath),
		Dir:          osutil.CanonicalizePath(extDir),
		RootDir:      osutil.CanonicalizePath(rootDir),
		Kind:         root.Kind,
		PluginPrefix: root.PluginPrefix,
		PluginRef:    pluginRef,
	}
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return plugins.IsExecutableFile(fileInfoDirEntry{info: info})
}

type fileInfoDirEntry struct{ info os.FileInfo }

func (d fileInfoDirEntry) Name() string               { return d.info.Name() }
func (d fileInfoDirEntry) IsDir() bool                { return d.info.IsDir() }
func (d fileInfoDirEntry) Type() os.FileMode          { return d.info.Mode().Type() }
func (d fileInfoDirEntry) Info() (os.FileInfo, error) { return d.info, nil }

type matcher struct {
	pluginRefs map[string]struct{}
	paths      map[string]struct{}
	empty      bool
}

func newMatcher(patterns []string, workingDir string) matcher {
	m := matcher{pluginRefs: map[string]struct{}{}, paths: map[string]struct{}{}, empty: len(patterns) == 0}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if looksLikePluginRef(pattern) {
			m.pluginRefs[pattern] = struct{}{}
			continue
		}
		m.paths[normalizePath(pattern, workingDir)] = struct{}{}
	}
	m.empty = len(m.pluginRefs) == 0 && len(m.paths) == 0
	return m
}

func (m matcher) allows(ext Extension) bool {
	return m.empty || m.matches(ext)
}

func (m matcher) matches(ext Extension) bool {
	if m.empty {
		return false
	}
	if ext.PluginRef != "" {
		if _, ok := m.pluginRefs[ext.PluginRef]; ok {
			return true
		}
	}
	if _, ok := m.paths[ext.ExecPath]; ok {
		return true
	}
	if _, ok := m.paths[ext.Dir]; ok {
		return true
	}
	return false
}

func looksLikePluginRef(value string) bool {
	return strings.Contains(value, "@") && !strings.HasPrefix(value, ".") && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "~")
}

func normalizePath(path, workingDir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	workingDir = strings.TrimSpace(workingDir)
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else if strings.HasPrefix(path, "~/") {
				path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
			}
		}
	}
	if !filepath.IsAbs(path) {
		if workingDir == "" {
			if wd, err := os.Getwd(); err == nil {
				workingDir = wd
			}
		}
		path = filepath.Join(workingDir, path)
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return osutil.CanonicalizePath(path)
}
