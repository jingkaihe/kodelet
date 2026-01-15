package hooks

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// CallbackResult contains the result of a callback execution
type CallbackResult struct {
	RecipeOutput string             `json:"recipe_output,omitempty"`
	Messages     []llmtypes.Message `json:"messages,omitempty"`
	Continue     bool               `json:"continue"`
}

// CallbackFunc is a function type for custom callback implementations
type CallbackFunc func(ctx context.Context, args map[string]string) (*CallbackResult, error)

// ThreadFactory is a function type for creating new threads for callback execution
type ThreadFactory func(ctx context.Context, config llmtypes.Config) (llmtypes.Thread, error)

// CallbackRegistry manages recipe-based callbacks and custom callback functions
type CallbackRegistry struct {
	fragmentProcessor *fragments.Processor
	threadFactory     ThreadFactory
	config            llmtypes.Config
	customCallbacks   map[string]CallbackFunc
}

// NewCallbackRegistry creates a registry with access to recipes
func NewCallbackRegistry(
	fp *fragments.Processor,
	tf ThreadFactory,
	config llmtypes.Config,
) *CallbackRegistry {
	return &CallbackRegistry{
		fragmentProcessor: fp,
		threadFactory:     tf,
		config:            config,
		customCallbacks:   make(map[string]CallbackFunc),
	}
}

// RegisterFunc registers a custom callback function
func (r *CallbackRegistry) RegisterFunc(name string, fn CallbackFunc) {
	r.customCallbacks[name] = fn
}

// Execute invokes a recipe or custom callback by name and returns the result
func (r *CallbackRegistry) Execute(ctx context.Context, recipeName string, args map[string]string) (*CallbackResult, error) {
	// Check for custom callback first
	if fn, ok := r.customCallbacks[recipeName]; ok {
		logger.G(ctx).WithField("callback", recipeName).Debug("executing custom callback")
		return fn(ctx, args)
	}

	// Fall back to recipe-based callback
	return r.executeRecipe(ctx, recipeName, args)
}

// executeRecipe loads and executes a recipe, returning the result
func (r *CallbackRegistry) executeRecipe(ctx context.Context, recipeName string, args map[string]string) (*CallbackResult, error) {
	if r.fragmentProcessor == nil {
		return nil, errors.New("fragment processor not configured")
	}

	// Load the recipe
	fragment, err := r.fragmentProcessor.LoadFragment(ctx, &fragments.Config{
		FragmentName: recipeName,
		Arguments:    args,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load recipe %s", recipeName)
	}

	if r.threadFactory == nil {
		return nil, errors.New("thread factory not configured")
	}

	// Create a thread to execute the recipe
	// Mark it with the recipe name so the hook knows the context
	config := r.config
	config.InvokedRecipe = recipeName

	thread, err := r.threadFactory(ctx, config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create thread for callback")
	}

	// Execute the recipe
	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = thread.SendMessage(ctx, fragment.Content, handler, llmtypes.MessageOpt{
		DisableAutoCompact: true, // Prevent infinite loop
		NoSaveConversation: true,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to execute recipe")
	}

	output := handler.CollectedText()

	logger.G(ctx).WithField("recipe", recipeName).WithField("output_length", len(output)).Debug("recipe callback executed")

	return &CallbackResult{
		RecipeOutput: output,
		Messages: []llmtypes.Message{
			{Role: "user", Content: output},
		},
		Continue: false,
	}, nil
}

// HasCallback returns true if a callback or recipe with the given name exists
func (r *CallbackRegistry) HasCallback(name string) bool {
	if _, ok := r.customCallbacks[name]; ok {
		return true
	}
	if r.fragmentProcessor != nil {
		_, err := r.fragmentProcessor.GetFragmentMetadata(name)
		return err == nil
	}
	return false
}
