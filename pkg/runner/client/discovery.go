package client

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

func (s *Service) workspaceCWDHints(ctx context.Context, params protocol.WorkspaceCWDHintsParams) (protocol.WorkspaceCWDHintsResult, error) {
	if s == nil {
		return protocol.WorkspaceCWDHintsResult{}, errors.New("runner service is required")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return protocol.WorkspaceCWDHintsResult{}, errors.New("runner service is closed")
	}
	base, err := s.instanceProvider.ResolveWorkingDirectory(ctx, params.CWD)
	if err != nil {
		return protocol.WorkspaceCWDHintsResult{}, err
	}
	if _, err := s.loadConfig(base, params.Profile, params.EnvironmentProfile); err != nil {
		return protocol.WorkspaceCWDHintsResult{}, err
	}
	query := strings.TrimSpace(params.Query)
	if len(query) > 4096 {
		return protocol.WorkspaceCWDHintsResult{}, errors.New("directory query exceeds 4096 bytes")
	}
	searchBase, filter := base, query
	if query != "" {
		if strings.HasSuffix(query, "/") || query == "~" {
			searchBase, filter = query, ""
		} else {
			searchBase, filter = filepath.Dir(query), filepath.Base(query)
		}
		if !filepath.IsAbs(searchBase) && !strings.HasPrefix(searchBase, "~") {
			searchBase = filepath.Join(base, searchBase)
		}
	}
	searchBase, err = s.instanceProvider.ResolveWorkingDirectory(ctx, searchBase)
	if err != nil {
		return protocol.WorkspaceCWDHintsResult{}, err
	}
	bases := []string{searchBase}
	if query != "" && !strings.ContainsAny(query, "/~") && filepath.Dir(base) != searchBase {
		bases = append(bases, filepath.Dir(base))
	}
	filter = strings.ToLower(filter)
	hints := []protocol.DirectoryHint{}
	seen := map[string]bool{}
	for _, directory := range bases {
		entries, err := os.ReadDir(directory)
		if err != nil {
			if directory == searchBase {
				return protocol.WorkspaceCWDHintsResult{}, errors.Wrap(err, "failed to read runner suggestion directory")
			}
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return protocol.WorkspaceCWDHintsResult{}, err
			}
			if !entry.IsDir() || !matchesDirectoryQuery(entry.Name(), filter) {
				continue
			}
			resolved, err := s.instanceProvider.ResolveWorkingDirectory(ctx, filepath.Join(directory, entry.Name()))
			if err == nil && !seen[resolved] {
				seen[resolved] = true
				hints = append(hints, protocol.DirectoryHint{Path: resolved})
			}
		}
	}
	sort.Slice(hints, func(i, j int) bool {
		left, right := strings.ToLower(filepath.Base(hints[i].Path)), strings.ToLower(filepath.Base(hints[j].Path))
		if left == right {
			return hints[i].Path < hints[j].Path
		}
		return left < right
	})
	if len(hints) > 20 {
		hints = hints[:20]
	}
	return protocol.WorkspaceCWDHintsResult{BaseDir: searchBase, Query: query, Hints: hints}, nil
}

func matchesDirectoryQuery(name, filter string) bool {
	wanted := []rune(filter)
	for _, char := range strings.ToLower(name) {
		if len(wanted) == 0 {
			return true
		}
		if char == wanted[0] {
			wanted = wanted[1:]
		}
	}
	return len(wanted) == 0
}
