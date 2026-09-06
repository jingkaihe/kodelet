package protocol

import (
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/pkg/errors"
)

// WorkspaceGitCommitSnapshot identifies the staged changes reviewed by a client.
// Diff is a bounded patch preview. When Truncated is true, DiffStat provides a
// bounded overview; Tree always identifies the entire staged tree.
type WorkspaceGitCommitSnapshot struct {
	CWD        string `json:"cwd"`
	GitRoot    string `json:"gitRoot"`
	Head       string `json:"head"`
	HeadRef    string `json:"headRef"`
	Tree       string `json:"tree"`
	Diff       string `json:"diff"`
	DiffStat   string `json:"diffStat,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	RunnerID   string `json:"runnerId"`
	Generation int64  `json:"generation"`
}

// WorkspaceGitCommitParams explicitly approves one previously prepared snapshot.
type WorkspaceGitCommitParams struct {
	CWD        string `json:"cwd"`
	Head       string `json:"head"`
	HeadRef    string `json:"headRef"`
	Tree       string `json:"tree"`
	Generation int64  `json:"generation"`
	Message    string `json:"message"`
	SignOff    bool   `json:"signOff"`
}

// Validate rejects malformed approvals before any repository access.
func (p WorkspaceGitCommitParams) Validate() error {
	if strings.TrimSpace(p.CWD) == "" || p.Generation <= 0 {
		return errors.New("commit requires the prepared directory and runner generation")
	}
	for _, oid := range []string{p.Tree, p.Head} {
		if oid == "" && p.Head == "" && p.Tree != "" {
			continue // An unborn branch has no parent commit.
		}
		if len(oid) != 40 && len(oid) != 64 {
			return errors.New("commit requires valid prepared tree and HEAD object IDs")
		}
		if _, err := hex.DecodeString(oid); err != nil {
			return errors.New("commit object IDs must be hexadecimal")
		}
	}
	if strings.TrimSpace(p.Message) == "" || len(p.Message) > 64*1024 || !utf8.ValidString(p.Message) || strings.ContainsRune(p.Message, 0) {
		return errors.New("commit message must be nonempty UTF-8 text of at most 64 KiB without NUL")
	}
	if len(p.HeadRef) > 1024 || strings.ContainsAny(p.HeadRef, "\x00\r\n") {
		return errors.New("invalid prepared HEAD reference")
	}
	return nil
}

// WorkspaceGitCommitResult confirms a runner-side Git mutation.
type WorkspaceGitCommitResult struct {
	Commit string `json:"commit"`
	Output string `json:"output"`
}
