package extensions

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/pkg/errors"
)

// ChildHost delegates extension-owned work through the authenticated runner.
type ChildHost interface {
	ChildRequest(context.Context, UIExtensionSource, string, json.RawMessage) (any, error)
}
type childHostKey struct{}

func ContextWithChildHost(ctx context.Context, host ChildHost) context.Context {
	return context.WithValue(ctx, childHostKey{}, host)
}

func (p *Process) setProfiles(source *processExtensionUISource, profiles []delegation.Profile) error {
	resolved := make(map[string]delegation.Preset, len(profiles))
	if len(profiles) > 32 {
		return errors.New("extension supports at most 32 execution presets")
	}
	for _, profile := range profiles {
		if err := profile.Validate(); err != nil {
			return err
		}
		if _, exists := resolved[profile.Name]; exists {
			return errors.New("duplicate extension execution preset")
		}
		profile.Options = profile.Options.Clone()
		if profile.SystemPromptPath != "" {
			path := profile.SystemPromptPath
			if !filepath.IsAbs(path) {
				path = filepath.Join(p.Extension.Dir, path)
			}
			info, err := os.Stat(path)
			if err != nil {
				return errors.Wrap(err, "failed to stat runner preset prompt")
			}
			if !info.Mode().IsRegular() {
				return errors.New("preset prompt must be a regular file")
			}
			file, err := os.Open(path)
			if err != nil {
				return errors.Wrap(err, "failed to open runner preset prompt")
			}
			content, err := io.ReadAll(io.LimitReader(file, 256*1024+1))
			_ = file.Close()
			if err != nil {
				return err
			}
			if len(content) > 256*1024 {
				return errors.New("preset prompt exceeds limit")
			}
			profile.SystemPrompt = string(content)
			profile.SystemPromptPath = ""
		}
		resolved[profile.Name] = delegation.Preset{Profile: profile, ExtensionID: p.Extension.ID, Generation: source.owner.Generation}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.uiSource != source || p.closed {
		return errors.New("extension generation changed during preset registration")
	}
	p.profiles = resolved
	return nil
}

// Profiles returns independent snapshots; closed/restarted generations cannot
// reuse old registrations. New initialization replaces, rather than merges, them.
func (r *Runtime) Profiles() []delegation.Preset {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var profiles []delegation.Preset
	for _, process := range r.processes {
		process.mu.Lock()
		if !process.closed && process.uiSource != nil {
			for _, profile := range process.profiles {
				if profile.Generation != process.uiSource.owner.Generation {
					continue
				}
				profile.Options = profile.Options.Clone()
				profiles = append(profiles, profile)
			}
		}
		process.mu.Unlock()
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].ExtensionID != profiles[j].ExtensionID {
			return profiles[i].ExtensionID < profiles[j].ExtensionID
		}
		return profiles[i].Name < profiles[j].Name
	})
	return profiles
}

func (t *Tool) ExtensionID() string { return t.extensionID }
