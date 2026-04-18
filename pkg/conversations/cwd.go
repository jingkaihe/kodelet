package conversations

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

// ErrCWDConflict is returned when a caller requests a cwd that conflicts with
// an existing conversation binding.
var ErrCWDConflict = errors.New("requested cwd does not match conversation cwd")

// CWDResolution captures the resolved execution directory for a conversation.
type CWDResolution struct {
	CWD            string
	Locked         bool
	LegacyRecord   bool
	ConversationID string
	Record         *convtypes.ConversationRecord
}

// NormalizeCWD resolves a path into a canonical absolute directory path.
func NormalizeCWD(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}

	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", errors.Wrapf(err, "failed to resolve cwd path: %s", trimmed)
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.Errorf("cwd directory does not exist: %s", absPath)
		}
		return "", errors.Wrapf(err, "failed to resolve cwd path: %s", absPath)
	}
	resolvedPath = osutil.CanonicalizePath(resolvedPath)

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to access cwd: %s", resolvedPath)
	}
	if !info.IsDir() {
		return "", errors.Errorf("cwd is not a directory: %s", resolvedPath)
	}

	return filepath.Clean(resolvedPath), nil
}

// CurrentWorkingDirectory returns the canonical current process working directory.
func CurrentWorkingDirectory() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve working directory")
	}
	return NormalizeCWD(workingDir)
}

// ResolveCWD determines the effective cwd for a new or existing conversation.
// When requireExisting is false, a missing conversation is treated as a new one.
func ResolveCWD(
	ctx context.Context,
	store ConversationStore,
	conversationID string,
	requestedCWD string,
	defaultCWD string,
	requireExisting bool,
) (*CWDResolution, error) {
	resolvedRequested, err := NormalizeCWD(requestedCWD)
	if err != nil {
		return nil, err
	}

	resolvedDefault, err := NormalizeCWD(defaultCWD)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(conversationID) == "" {
		cwd := resolvedRequested
		if cwd == "" {
			cwd = resolvedDefault
		}
		if cwd == "" {
			return nil, errors.New("working directory could not be resolved")
		}
		return &CWDResolution{CWD: cwd}, nil
	}

	record, err := store.Load(ctx, conversationID)
	if err != nil {
		if requireExisting {
			return nil, err
		}

		cwd := resolvedRequested
		if cwd == "" {
			cwd = resolvedDefault
		}
		if cwd == "" {
			return nil, errors.New("working directory could not be resolved")
		}
		return &CWDResolution{
			CWD:            cwd,
			ConversationID: conversationID,
		}, nil
	}

	if strings.TrimSpace(record.CWD) != "" {
		storedCWD, err := NormalizeCWD(record.CWD)
		if err != nil {
			return nil, err
		}
		if resolvedRequested != "" && resolvedRequested != storedCWD {
			return nil, errors.Wrapf(
				ErrCWDConflict,
				"conversation %s is bound to %s, not %s",
				conversationID,
				storedCWD,
				resolvedRequested,
			)
		}
		return &CWDResolution{
			CWD:            storedCWD,
			Locked:         true,
			ConversationID: conversationID,
			Record:         &record,
		}, nil
	}

	cwd := resolvedRequested
	if cwd == "" {
		cwd = resolvedDefault
	}
	if cwd == "" {
		return nil, errors.New("working directory could not be resolved")
	}

	return &CWDResolution{
		CWD:            cwd,
		Locked:         true,
		LegacyRecord:   true,
		ConversationID: conversationID,
		Record:         &record,
	}, nil
}
