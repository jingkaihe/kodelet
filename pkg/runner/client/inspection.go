package client

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

func (s *Service) inspectionRuntime(ctx context.Context, cwd, environmentProfile string, config llmtypes.Config, extensionConfig extensions.Config) (*extensions.Runtime, func() error, error) {
	variant := normalizeEnvironmentProfile(environmentProfile)
	if provider, ok := s.runtimeProvider.(isolatedRuntimeDiscoveryProvider); ok {
		return provider.RuntimeForCommandDiscoveryWithIsolatedLease(ctx, cwd, variant, extensionConfig)
	}
	if provider, ok := s.runtimeProvider.(runtimeDiscoveryProvider); ok {
		runtime, err := provider.RuntimeForCommandDiscoveryWithConfig(ctx, cwd, variant, extensionConfig)
		return runtime, nil, err
	}
	runtime, err := s.runtimeProvider.RuntimeWithConfigAndCallContext(ctx, cwd, variant, extensionConfig, extensions.ExtensionCallContext{
		ConversationID: "runner-manifest-probe", UIScopeID: "runner-manifest-probe", CWD: cwd,
		Provider: config.Provider, Model: config.Model, Profile: config.Profile, RecipeName: config.RecipeName, InvokedBy: "runner.manifest",
	})
	return runtime, nil, err
}

func (s *Service) inspectWorkspace(ctx context.Context, params protocol.WorkspaceInspectParams) (result protocol.WorkspaceInspectResult, inspectErr error) {
	if err := params.Validate(); err != nil {
		return result, err
	}
	if err := s.lockSnapshot(ctx); err != nil {
		return result, err
	}
	defer s.unlockSnapshot()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return result, errors.New("runner service is closed")
	}
	cwd, err := s.instanceProvider.ResolveWorkingDirectory(ctx, params.CWD)
	if err != nil {
		return result, err
	}
	instance, err := s.instanceProvider.Create(ctx, ExecutionInstanceSpec{RunID: "runner-inspection", ConversationID: "runner-inspection", CWD: cwd, Probe: true})
	if err != nil {
		return result, err
	}
	if instance == nil {
		return result, errors.New("could not prepare the workspace for inspection")
	}
	defer func() { inspectErr = s.closeProbeResources(ctx, nil, instance, inspectErr) }()
	if instance.WorkingDirectory() != cwd {
		return result, errors.New("the inspection working directory differs from the requested directory")
	}
	config, err := s.loadConfig(cwd, params.Profile, params.EnvironmentProfile)
	if err != nil {
		return result, err
	}
	config.WorkingDirectory = cwd
	extensionConfig, err := extensions.LoadConfigFromSettings(config.ExtensionSettings)
	if err != nil {
		return result, err
	}
	result.CWD, result.EnvironmentProfile = cwd, normalizeEnvironmentProfile(params.EnvironmentProfile)
	if strings.HasPrefix(params.Operation, "extension.") {
		discovery, err := extensions.NewDiscovery(extensions.WithWorkingDir(cwd), extensions.WithConfig(extensionConfig))
		if err != nil {
			return result, err
		}
		found, err := discovery.Discover()
		if err != nil {
			return result, err
		}
		result.Extensions = []protocol.ExtensionInfo{}
		for _, ext := range found {
			info := protocol.ExtensionInfo{ID: ext.ID, Name: ext.Name, Source: string(ext.Kind), Path: ext.ExecPath, Directory: ext.Dir, PluginRef: ext.PluginRef}
			if params.Operation == "extension.list" {
				result.Extensions = append(result.Extensions, info)
				continue
			}
			name := strings.TrimSpace(params.Name)
			if name == info.ID || name == info.Name || name == info.Path || name == info.Directory || (info.PluginRef != "" && name == info.PluginRef) {
				result.Extension = &info
				return result, nil
			}
		}
		if params.Operation == "extension.inspect" {
			return result, errors.Errorf("extension not found: %s", params.Name)
		}
		return result, nil
	}
	processor, err := fragments.NewFragmentProcessor(fragments.WithDefaultDirsForCWD(cwd))
	if err != nil {
		return result, err
	}
	if params.Operation == "recipe.show" {
		result.Recipe, err = processor.LoadFragment(ctx, &fragments.Config{FragmentName: params.Name, Arguments: params.Arguments})
		return result, err
	}
	result.Recipes, err = processor.ListFragmentsWithMetadata()
	if err != nil {
		return result, err
	}
	// Listing includes dynamic recipes, but never renders templates or opens a model turn.
	probeCtx := s.decorateRunContext(ctx, "runner-manifest-probe", "runner-manifest-probe")
	capabilities := extensions.RuntimeCapabilitiesFromContext(probeCtx)
	capabilities.BackgroundTasks = false
	probeCtx = extensions.ContextWithRuntimeCapabilities(probeCtx, capabilities)
	probeCtx, cancel := context.WithCancel(probeCtx)
	defer cancel()
	runtime, release, err := s.inspectionRuntime(probeCtx, cwd, params.EnvironmentProfile, config, extensionConfig)
	if release != nil {
		defer func() {
			inspectErr = combineCleanupErrors(inspectErr, runBoundedCleanup(context.WithoutCancel(probeCtx), s.cleanupTimeout, "recipe discovery", func(context.Context) error { return release() }))
		}()
	}
	if err != nil {
		return result, errors.Wrap(err, "could not discover extension recipes")
	}
	if runtime != nil {
		for _, command := range runtime.Commands() {
			if command.Registration.Kind != "recipe" {
				continue
			}
			name := command.Registration.Name
			result.Recipes = append(result.Recipes, &fragments.Fragment{ID: name, Path: "extension:" + command.ExtensionID + "/" + name, Metadata: fragments.Metadata{Name: name, Description: command.Registration.Description}})
		}
	}
	return result, nil
}
