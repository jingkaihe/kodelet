package controlplane

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// Definitions are live runner resources, not mutations to global daemon config.
// A new runner generation must register again; conversations keep their snapshots.
type extensionProfileKey struct {
	principalID string
	runnerID    string
	name        string
}

type registeredExtensionProfile struct {
	profile    extensions.Profile
	generation int64
}

type profileRegisteringController struct {
	agentenv.RemoteController
	server *Server
}

func (c profileRegisteringController) OpenRun(ctx context.Context, runnerID string, params protocol.RunOpenParams) (runnerpayload.Manifest, error) {
	manifest, err := c.RemoteController.OpenRun(ctx, runnerID, params)
	if err != nil {
		return runnerpayload.Manifest{}, err
	}
	if err := c.server.registerExtensionProfiles(ctx, manifest); err != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = c.CloseRun(closeCtx, params.RunID, protocol.RunStatusFailed, err)
		return runnerpayload.Manifest{}, errors.Wrap(err, "failed to register extension profiles")
	}
	return manifest, nil
}

func (s *Server) registerExtensionProfiles(ctx context.Context, manifest runnerpayload.Manifest) error {
	if len(manifest.Profiles) == 0 {
		return nil
	}
	principal, ok := principalFromContext(ctx)
	if !ok || principal.ID == "" {
		return errors.New("extension profile registration requires an authenticated client")
	}
	if s.runnerRegistry == nil {
		return errors.New("runner registry is unavailable")
	}
	runner, ok := s.runnerRegistry.Runner(manifest.RunnerID)
	if !ok || !runner.Connected || runner.Generation != manifest.Generation {
		return errors.New("extension profile registration is from an inactive runner generation")
	}
	profiles := make(map[extensionProfileKey]registeredExtensionProfile, len(manifest.Profiles))
	for _, profile := range manifest.Profiles {
		if _, err := chat.ResolveExtensionProfile(profile, ""); err != nil {
			return errors.Wrapf(err, "invalid extension profile %q", profile.Name)
		}
		if llm.HasConfiguredProfile(profile.Name) {
			return errors.Errorf("extension profile %q conflicts with a daemon-configured profile", profile.Name)
		}
		key := extensionProfileKey{principal.ID, runner.ID, profile.Name}
		if _, duplicate := profiles[key]; duplicate {
			return errors.Errorf("duplicate extension profile %q", profile.Name)
		}
		profile.Options = profile.Options.Clone()
		profiles[key] = registeredExtensionProfile{profile: profile, generation: runner.Generation}
	}

	return s.publishExtensionProfiles(principal.ID, runner.ID, runner.Generation, profiles)
}

func (s *Server) publishExtensionProfiles(principalID, runnerID string, generation int64, profiles map[extensionProfileKey]registeredExtensionProfile) error {
	s.extensionProfilesMu.Lock()
	defer s.extensionProfilesMu.Unlock()
	runner, ok := s.runnerRegistry.Runner(runnerID)
	if !ok || !runner.Connected || runner.Generation != generation {
		return errors.New("extension profile registration is from an inactive runner generation")
	}
	count := len(profiles)
	for key, existing := range s.extensionProfiles {
		if key.principalID != principalID || key.runnerID != runner.ID || existing.generation != runner.Generation {
			continue
		}
		if profile, exists := profiles[key]; exists {
			if existing.profile.ExtensionID != profile.profile.ExtensionID {
				return errors.Errorf("extension profile %q is already registered by %s", key.name, existing.profile.ExtensionID)
			}
		} else {
			count++
		}
	}
	if count > 256 {
		return errors.New("a caller may register at most 256 profiles per runner")
	}
	if s.extensionProfiles == nil {
		s.extensionProfiles = make(map[extensionProfileKey]registeredExtensionProfile)
	}
	for key, existing := range s.extensionProfiles {
		if key.runnerID == runner.ID && existing.generation < runner.Generation {
			delete(s.extensionProfiles, key)
		}
	}
	for key, profile := range profiles {
		s.extensionProfiles[key] = profile
	}
	return nil
}

func (s *Server) resolveModelProfile(ctx context.Context, runnerID, name, effort string) (llmtypes.Config, error) {
	name = strings.TrimSpace(name)
	if chat.NormalizeRequestedProfile(name) == "" || llm.HasConfiguredProfile(name) {
		return chat.ResolveConfigForNewConversation(name, effort)
	}
	principal, ok := principalFromContext(ctx)
	if !ok || principal.ID == "" {
		return llmtypes.Config{}, errors.New("extension profile selection requires an authenticated client")
	}
	if s.runnerRegistry == nil {
		return llmtypes.Config{}, errors.New("runner registry is unavailable")
	}
	runner, ok := s.runnerRegistry.Runner(runnerID)
	if !ok || !runner.Connected {
		return llmtypes.Config{}, errors.New("the selected runner is unavailable")
	}
	profile, exists := s.registeredProfile(extensionProfileKey{principal.ID, runnerID, name}, runner.Generation)
	if !exists {
		return llmtypes.Config{}, errors.Errorf("extension profile %q is not registered for this caller and runner; initialize its extension on this runner first", name)
	}
	return chat.ResolveExtensionProfile(profile, effort)
}

func (s *Server) registeredProfile(key extensionProfileKey, generation int64) (extensions.Profile, bool) {
	s.extensionProfilesMu.RLock()
	defer s.extensionProfilesMu.RUnlock()
	registered, exists := s.extensionProfiles[key]
	if !exists || registered.generation != generation {
		return extensions.Profile{}, false
	}
	return registered.profile.Clone(), true
}

func (s *Server) modelProfileOptions(ctx context.Context, runnerID, selected string, includeHidden bool) []ChatProfileOption {
	options := getWebUIProfileOptions()
	if includeHidden {
		for name := range llm.ProfileSources() {
			if llm.IsProfileHidden(name) {
				options = append(options, ChatProfileOption{Name: name, Scope: "configured", Hidden: true})
			}
		}
	}
	principal, authenticated := principalFromContext(ctx)
	if authenticated && s.runnerRegistry != nil {
		if runner, ok := s.runnerRegistry.Runner(runnerID); ok && runner.Connected {
			s.extensionProfilesMu.RLock()
			for key, registered := range s.extensionProfiles {
				if key.principalID != principal.ID || key.runnerID != runnerID || registered.generation != runner.Generation || (registered.profile.Hidden && !includeHidden) {
					continue
				}
				options = append(options, ChatProfileOption{Name: key.name, Scope: "extension", Hidden: registered.profile.Hidden})
			}
			s.extensionProfilesMu.RUnlock()
		}
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Name == "default" || options[j].Name == "default" {
			return options[i].Name == "default"
		}
		return options[i].Name < options[j].Name
	})
	for i := range options {
		options[i].Active = options[i].Name == selected
	}
	return options
}
