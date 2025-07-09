package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// WatchConfig holds configuration for the watch command
type WatchConfig struct {
	IgnoreDirs     []string
	IncludePattern string
	Verbosity      string
	DebounceTime   int
	UseWeakModel   bool
}

// NewWatchConfig creates a new WatchConfig with default values
func NewWatchConfig() *WatchConfig {
	return &WatchConfig{
		IgnoreDirs:     []string{".git", "node_modules"},
		IncludePattern: "",
		Verbosity:      "normal",
		DebounceTime:   500,
		UseWeakModel:   false,
	}
}

// Validate validates the WatchConfig and returns an error if invalid
func (c *WatchConfig) Validate() error {
	validVerbosityLevels := []string{"quiet", "normal", "verbose"}
	for _, level := range validVerbosityLevels {
		if c.Verbosity == level {
			goto verbosityValid
		}
	}
	return errors.New(fmt.Sprintf("invalid verbosity level: %s, must be one of: %s", c.Verbosity, strings.Join(validVerbosityLevels, ", ")))

verbosityValid:
	if c.DebounceTime < 0 {
		return errors.New(fmt.Sprintf("debounce time cannot be negative: %d", c.DebounceTime))
	}

	return nil
}

// FileEvent represents a file system event with additional metadata
type FileEvent struct {
	Path string
	Op   fsnotify.Op
	Time time.Time
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch for file changes and provide AI assistance",
	Long: `Continuously monitors file changes in the current directory and provides
AI-powered insights or assistance whenever changes are detected.

By default, it watches the current directory and all subdirectories,
ignoring common directories like .git and node_modules.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Create a cancellable context that listens for signals
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// Get watch config from flags first to configure quiet mode
		config := getWatchConfigFromFlags(cmd)

		// Configure presenter based on verbosity
		presenter.SetQuiet(config.Verbosity == "quiet")

		// Set up signal handling
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			presenter.Warning("\n[kodelet]: Cancellation requested, shutting down...")
			cancel()
		}()

		// Create the MCP manager from Viper configuration
		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create MCP manager")
			return
		}

		llmConfig := llm.GetConfigFromViper()
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig), tools.WithMCPTools(mcpManager))

		// Validate configuration
		if err := config.Validate(); err != nil {
			presenter.Error(err, "Invalid configuration")
			os.Exit(1)
		}

		runWatchMode(ctx, s, config)
	},
}

func init() {
	defaults := NewWatchConfig()
	watchCmd.Flags().StringSliceP("ignore", "i", defaults.IgnoreDirs, "Directories to ignore")
	watchCmd.Flags().StringP("include", "p", defaults.IncludePattern, "File pattern to include (e.g., '*.go', '*.{js,ts}')")
	watchCmd.Flags().StringP("verbosity", "v", defaults.Verbosity, "Verbosity level (quiet, normal, verbose)")
	watchCmd.Flags().IntP("debounce", "d", defaults.DebounceTime, "Debounce time in milliseconds for file change events")
	watchCmd.Flags().Bool("use-weak-model", defaults.UseWeakModel, "Use auto-completion model")
}

// getWatchConfigFromFlags extracts watch configuration from command flags
func getWatchConfigFromFlags(cmd *cobra.Command) *WatchConfig {
	config := NewWatchConfig()

	if ignoreDirs, err := cmd.Flags().GetStringSlice("ignore"); err == nil {
		config.IgnoreDirs = ignoreDirs
	}
	if includePattern, err := cmd.Flags().GetString("include"); err == nil {
		config.IncludePattern = includePattern
	}
	if verbosity, err := cmd.Flags().GetString("verbosity"); err == nil {
		config.Verbosity = verbosity
	}
	if debounceTime, err := cmd.Flags().GetInt("debounce"); err == nil {
		config.DebounceTime = debounceTime
	}
	if useWeakModel, err := cmd.Flags().GetBool("use-weak-model"); err == nil {
		config.UseWeakModel = useWeakModel
	}

	return config
}

func runWatchMode(ctx context.Context, state tooltypes.State, config *WatchConfig) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		presenter.Error(err, "Failed to create file watcher")
		logger.G(ctx).WithError(err).Fatal("Failed to create file watcher")
	}
	defer watcher.Close()

	// Setup debouncing mechanism
	events := make(chan FileEvent)
	debouncedEvents := make(chan FileEvent)

	// Start debouncer goroutine
	go debounceFileEvents(ctx, events, debouncedEvents, time.Duration(config.DebounceTime)*time.Millisecond)

	// Process events
	go func() {
		for {
			select {
			case event, ok := <-debouncedEvents:
				if !ok {
					return
				}
				if config.Verbosity != "quiet" {
					presenter.Info(fmt.Sprintf("Change detected: %s (%s)", event.Path, event.Op))
					logger.G(ctx).WithFields(map[string]interface{}{
						"file":      event.Path,
						"operation": event.Op.String(),
						"timestamp": event.Time,
					}).Debug("File change detected")
				}
				processFileChange(ctx, state, event.Path, config)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Watch for events
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Skip ignored directories
				skipEvent := false
				for _, ignoreDir := range config.IgnoreDirs {
					if strings.Contains(event.Name, ignoreDir+string(os.PathSeparator)) {
						skipEvent = true
						break
					}
				}
				if skipEvent {
					continue
				}

				// Only process write and create events
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					// Skip binary files
					if utils.IsBinaryFile(event.Name) {
						if config.Verbosity == "verbose" {
							presenter.Info(fmt.Sprintf("Skipping binary file: %s", event.Name))
							logger.G(ctx).WithField("file", event.Name).Debug("Skipped binary file")
						}
						continue
					}

					// Check if file matches include pattern
					if config.IncludePattern != "" {
						matched, err := filepath.Match(config.IncludePattern, filepath.Base(event.Name))
						if err != nil || !matched {
							if config.Verbosity == "verbose" && err != nil {
								logger.G(ctx).WithError(err).WithField("pattern", config.IncludePattern).Debug("Pattern matching error")
							}
							continue
						}
					}
					events <- FileEvent{
						Path: event.Name,
						Op:   event.Op,
						Time: time.Now(),
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				presenter.Error(err, "File watcher error")
				logger.G(ctx).WithError(err).Error("Error watching files")
			case <-ctx.Done():
				return
			}
		}
	}()

	// Add current directory and subdirectories to watcher
	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip ignored directories
			for _, ignoreDir := range config.IgnoreDirs {
				if strings.Contains(path, ignoreDir+string(os.PathSeparator)) || path == ignoreDir {
					if config.Verbosity == "verbose" {
						logger.G(ctx).WithField("directory", path).Debug("Skipping ignored directory")
					}
					return filepath.SkipDir
				}
			}
			logger.G(ctx).WithField("directory", path).Debug("Adding directory to watcher")
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		presenter.Error(err, "Failed to watch directories")
		logger.G(ctx).WithError(err).Fatal("Failed to watch directories")
	}

	presenter.Info("Watching for file changes... Press Ctrl+C to stop")
	logger.G(ctx).WithField("directories_count", len(config.IgnoreDirs)).Info("File watcher initialized")

	// Wait for context cancellation
	<-ctx.Done()
}

// Debounce file events to prevent processing multiple rapid changes to the same file
func debounceFileEvents(ctx context.Context, input <-chan FileEvent, output chan<- FileEvent, delay time.Duration) {
	var pending = make(map[string]*time.Timer)

	for {
		select {
		case event, ok := <-input:
			if !ok {
				// Clean up pending timers before returning
				for _, timer := range pending {
					timer.Stop()
				}
				return
			}
			// Cancel any pending timers for this file
			if timer, exists := pending[event.Path]; exists {
				timer.Stop()
				delete(pending, event.Path)
			}

			// Create a new timer
			eventCopy := event // Create a copy of the event to avoid race conditions
			pending[event.Path] = time.AfterFunc(delay, func() {
				select {
				case output <- eventCopy:
					delete(pending, eventCopy.Path)
				case <-ctx.Done():
					// Context cancelled, don't send the event
					delete(pending, eventCopy.Path)
				}
			})
		case <-ctx.Done():
			// Clean up pending timers before returning
			for _, timer := range pending {
				timer.Stop()
			}
			return
		}
	}
}

var (
	MagicCommentPatterns = []string{"# @kodelet", "// @kodelet"}
)

// Process a file change event
func processFileChange(ctx context.Context, state tooltypes.State, path string, config *WatchConfig) {
	// Double-check that the file is not binary before processing
	if utils.IsBinaryFile(path) {
		if config.Verbosity == "verbose" {
			presenter.Info(fmt.Sprintf("Skipping binary file processing: %s", path))
			logger.G(ctx).WithField("file", path).Debug("Skipped binary file processing")
		}
		return
	}

	// Read the file content
	content, err := os.ReadFile(path)
	if err != nil {
		presenter.Error(err, fmt.Sprintf("Failed to read file: %s", path))
		logger.G(ctx).WithError(err).WithField("file", path).Error("Failed to read file")
		return
	}

	// continue if the pattern is not found
	found := false
	foundPattern := ""
	for _, pattern := range MagicCommentPatterns {
		if strings.Contains(string(content), pattern) {
			found = true
			foundPattern = pattern
			break
		}
	}
	if !found {
		logger.G(ctx).WithField("file", path).Debug("No magic comment pattern found, skipping")
		return
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"file":           path,
		"pattern":        foundPattern,
		"content_length": len(content),
	}).Info("Processing file with magic comment")

	// Create query with file content and context
	query := fmt.Sprintf(`Here is the file "%s" that has just been changed.
Please analyze the changes and provide feedback.

Here is the content of the file:

==========
%s
==========

You might have noticed the "# @kodelet: do xyz" or "// @kodelet: do xyz" pattern.
This is a special comment that tells kodelet to make a change to the file.

Please make the change to the file that fulfills "xyz".

!IMPORTANT: Please make sure that "# @kodelet: do xyz" or "// @kodelet: do xyz" is removed after the change has been made.

# Examples
<example>
<before>
# @kodelet replace add with multiply
def add(a, b):
    return a + b
</before>
<after>
def multiply(a, b):
    return a * b
</after>
</example>
`,
		path, string(content))

	// Process with AI
	if config.Verbosity == "verbose" {
		presenter.Info("Sending to AI for analysis...")
	}

	// Get configuration for the LLM
	llmConfig := llm.GetConfigFromViper()

	var response string
	var usage llmtypes.Usage

	// Use the auto-completion model if appropriate
	if config.UseWeakModel {
		if config.Verbosity == "verbose" {
			presenter.Info(fmt.Sprintf("Using auto-completion model: %v", llmConfig.WeakModel))
		}
		logger.G(ctx).WithField("model", llmConfig.WeakModel).Debug("Using weak model for processing")
	}

	state.SetFileLastAccessed(path, time.Now())
	response, usage = llm.SendMessageAndGetTextWithUsage(ctx, state, query, llmConfig, false, llmtypes.MessageOpt{
		UseWeakModel: config.UseWeakModel,
		PromptCache:  false,
	})

	// Display the AI response
	presenter.Separator()
	presenter.Section(fmt.Sprintf("AI Analysis for %s", path))
	fmt.Println(response)

	// Display usage statistics using presenter package
	usageStats := presenter.ConvertUsageStats(&usage)
	presenter.Stats(usageStats)
	logger.G(ctx).WithFields(map[string]interface{}{
		"file":          path,
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
		"total_cost":    usage.TotalCost(),
	}).Info("AI analysis completed")

	presenter.Separator()
}
