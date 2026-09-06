package client

import (
	"context"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

func (s *Service) executeShortcut(ctx context.Context, params runnerpayload.ShortcutExecuteParams) (runnerpayload.ShortcutExecuteResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return runnerpayload.ShortcutExecuteResult{}, err
	}
	defer finish()
	s.mu.Lock()
	manifest, config, runtime := run.manifest, run.config, run.runtime
	s.mu.Unlock()
	if params.Digest == "" || params.Digest != manifest.Digest {
		return runnerpayload.ShortcutExecuteResult{}, errors.New("the available shortcuts changed; reload them before trying again")
	}
	matched := false
	for _, descriptor := range manifest.Shortcuts {
		if descriptor.Key == params.Shortcut.Key && descriptor.ExtensionID == params.Shortcut.ExtensionID && descriptor.Generation == params.Shortcut.Generation {
			matched = true
			break
		}
	}
	if !matched || params.Shortcut.Generation == 0 {
		return runnerpayload.ShortcutExecuteResult{}, errors.New("this shortcut is not available in the current run")
	}
	matched, result, err := runtime.ExecutePinnedShortcut(operationCtx, extensions.Shortcut{
		Key: params.Shortcut.Key, ExtensionID: params.Shortcut.ExtensionID, Generation: params.Shortcut.Generation,
	}, extensions.ExtensionCallContext{
		ConversationID: run.conversationID, UIScopeID: run.conversationID, CWD: manifest.WorkingDirectory,
		Provider: config.Provider, Model: config.Model, Profile: config.Profile, RecipeName: config.RecipeName, InvokedBy: run.invokedBy,
	})
	return runnerpayload.ShortcutExecuteResult{Matched: matched, Result: result}, err
}
