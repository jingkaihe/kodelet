// Package messagehistory persists raw user-submitted chat messages for local
// composer recall.
package messagehistory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	conversationstore "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
	"github.com/rogpeppe/go-internal/lockedfile"
)

const (
	// MaxEntriesPerScope is the hard cap for persisted messages in one CWD scope.
	MaxEntriesPerScope = 1000

	historyVersion = 1
)

// Entry is one raw user-submitted composer message.
type Entry struct {
	Version        int       `json:"v"`
	CreatedAt      time.Time `json:"ts"`
	ScopeCWD       string    `json:"scope_cwd"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Profile        string    `json:"profile,omitempty"`
	Source         string    `json:"source"`
	Text           string    `json:"text"`
}

// Store writes JSONL history files under the Kodelet base directory.
type Store struct {
	basePath string
}

// NewStore returns a store rooted in Kodelet's private base directory. It does
// not create directories until a message is appended.
func NewStore() (*Store, error) {
	basePath, err := defaultBasePath()
	if err != nil {
		return nil, err
	}
	return NewStoreWithBasePath(basePath), nil
}

// NewStoreWithBasePath returns a store rooted at basePath.
func NewStoreWithBasePath(basePath string) *Store {
	return &Store{basePath: osutil.CanonicalizePath(filepath.Clean(strings.TrimSpace(basePath)))}
}

// ResolveScopeCWD normalizes cwd and, when cwd is inside a Git worktree, returns
// the worktree root so history is shared across subdirectories of one project.
func ResolveScopeCWD(cwd string) (string, error) {
	normalized, err := conversationstore.NormalizeCWD(cwd)
	if err != nil || strings.TrimSpace(normalized) == "" {
		return normalized, err
	}

	if root, ok := gitRoot(normalized); ok {
		return root, nil
	}
	return normalized, nil
}

// Append records entry and keeps only MaxEntriesPerScope newest valid entries
// for the entry's CWD scope. Adjacent duplicate text is ignored.
func (s *Store) Append(ctx context.Context, entry Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return errors.New("message history store is nil")
	}

	entry.ScopeCWD = strings.TrimSpace(entry.ScopeCWD)
	entry.Text = strings.TrimSpace(entry.Text)
	if entry.ScopeCWD == "" || entry.Text == "" {
		return nil
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	} else {
		entry.CreatedAt = entry.CreatedAt.UTC()
	}
	entry.Version = historyVersion
	entry.Source = strings.TrimSpace(entry.Source)
	if entry.Source == "" {
		entry.Source = "tui"
	}

	path := s.pathForScope(entry.ScopeCWD)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.Wrap(err, "failed to create message history directory")
	}

	if err := lockedfile.Transform(path, func(data []byte) ([]byte, error) {
		entries := parseEntries(data)
		if len(entries) > 0 && entries[len(entries)-1].Text == entry.Text {
			return data, nil
		}
		entries = append(entries, entry)
		if len(entries) > MaxEntriesPerScope {
			entries = entries[len(entries)-MaxEntriesPerScope:]
		}
		return marshalEntries(entries)
	}); err != nil {
		return errors.Wrap(err, "failed to update message history")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return errors.Wrap(err, "failed to set message history permissions")
	}
	return nil
}

// List returns up to limit newest entries for scopeCWD, in chronological order.
func (s *Store) List(ctx context.Context, scopeCWD string, limit int) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("message history store is nil")
	}
	scopeCWD = strings.TrimSpace(scopeCWD)
	if scopeCWD == "" || limit == 0 {
		return nil, nil
	}
	if limit < 0 || limit > MaxEntriesPerScope {
		limit = MaxEntriesPerScope
	}

	entries, err := s.readEntries(scopeCWD)
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

func (s *Store) readEntries(scopeCWD string) ([]Entry, error) {
	path := s.pathForScope(scopeCWD)
	data, err := lockedfile.Read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "failed to read message history")
	}

	return parseEntries(data), nil
}

func parseEntries(data []byte) []Entry {
	entries := make([]Entry, 0)
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entry.ScopeCWD = strings.TrimSpace(entry.ScopeCWD)
		entry.Text = strings.TrimSpace(entry.Text)
		if entry.ScopeCWD == "" || entry.Text == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func marshalEntries(entries []Entry) ([]byte, error) {
	var buffer bytes.Buffer
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return nil, errors.Wrap(err, "failed to encode message history entry")
		}
		if _, err := buffer.Write(data); err != nil {
			return nil, errors.Wrap(err, "failed to write message history entry")
		}
		if err := buffer.WriteByte('\n'); err != nil {
			return nil, errors.Wrap(err, "failed to write message history newline")
		}
	}
	return buffer.Bytes(), nil
}

func (s *Store) pathForScope(scopeCWD string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(scopeCWD)))
	filename := hex.EncodeToString(sum[:]) + ".jsonl"
	return filepath.Join(s.basePath, "message-history", "by-cwd", filename)
}

func defaultBasePath() (string, error) {
	if basePath := strings.TrimSpace(os.Getenv("KODELET_BASE_PATH")); basePath != "" {
		return basePath, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".kodelet"), nil
}

func gitRoot(cwd string) (string, bool) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", false
	}
	cmd := exec.Command(gitPath, "-C", cwd, "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", false
	}
	root = osutil.CanonicalizePath(filepath.Clean(root))
	return root, true
}
