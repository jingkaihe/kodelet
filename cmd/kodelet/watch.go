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
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	ignoreDirs     []string
	includePattern string
	verbosity      string
	debounceTime   int
	useWeakModel   bool
)

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
		ctx := cmd.Context()
		s := state.NewBasicState()
		runWatchMode(ctx, s)
	},
}

func init() {
	watchCmd.Flags().StringSliceVarP(&ignoreDirs, "ignore", "i", []string{".git", "node_modules"}, "Directories to ignore")
	watchCmd.Flags().StringVarP(&includePattern, "include", "p", "", "File pattern to include (e.g., '*.go', '*.{js,ts}')")
	watchCmd.Flags().StringVarP(&verbosity, "verbosity", "v", "normal", "Verbosity level (quiet, normal, verbose)")
	watchCmd.Flags().IntVarP(&debounceTime, "debounce", "d", 500, "Debounce time in milliseconds for file change events")
	watchCmd.Flags().BoolVar(&useWeakModel, "use-weak-model", false, "Use auto-completion model")
}

func runWatchMode(ctx context.Context, s state.State) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create file watcher")
	}
	defer watcher.Close()

	// Setup done channel for cleanup
	done := make(chan bool)

	// Setup debouncing mechanism
	events := make(chan FileEvent)
	debouncedEvents := make(chan FileEvent)

	// Start debouncer goroutine
	go debounceFileEvents(events, debouncedEvents, time.Duration(debounceTime)*time.Millisecond)

	// Process events
	go func() {
		for {
			select {
			case event, ok := <-debouncedEvents:
				if !ok {
					return
				}
				if verbosity != "quiet" {
					fmt.Printf("Change detected: %s (%s)\n", event.Path, event.Op)
				}
				processFileChange(ctx, s, event.Path, event.Op)
			case <-done:
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
				for _, ignoreDir := range ignoreDirs {
					if strings.Contains(event.Name, ignoreDir+string(os.PathSeparator)) {
						continue
					}
				}
				// Only process write and create events
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					// Skip binary files
					if utils.IsBinaryFile(event.Name) {
						if verbosity == "verbose" {
							fmt.Printf("Skipping binary file: %s\n", event.Name)
						}
						continue
					}

					// Check if file matches include pattern
					if includePattern != "" {
						matched, err := filepath.Match(includePattern, filepath.Base(event.Name))
						if err != nil || !matched {
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
				logrus.WithError(err).Error("Error watching files")
			case <-done:
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
			for _, ignoreDir := range ignoreDirs {
				if strings.Contains(path, ignoreDir+string(os.PathSeparator)) || path == ignoreDir {
					return filepath.SkipDir
				}
			}
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to watch directories")
	}

	fmt.Println("Watching for file changes... Press Ctrl+C to stop")

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	close(done)
}

// Debounce file events to prevent processing multiple rapid changes to the same file
func debounceFileEvents(input <-chan FileEvent, output chan<- FileEvent, delay time.Duration) {
	var pending = make(map[string]*time.Timer)

	for event := range input {
		// Cancel any pending timers for this file
		if timer, exists := pending[event.Path]; exists {
			timer.Stop()
			delete(pending, event.Path)
		}

		// Create a new timer
		eventCopy := event // Create a copy of the event to avoid race conditions
		pending[event.Path] = time.AfterFunc(delay, func() {
			output <- eventCopy
			delete(pending, eventCopy.Path)
		})
	}
}

var (
	MagicCommentPatterns = []string{"# @kodelet", "// @kodelet"}
)

// Process a file change event
func processFileChange(ctx context.Context, s state.State, path string, op fsnotify.Op) {
	// Double-check that the file is not binary before processing
	if utils.IsBinaryFile(path) {
		if verbosity == "verbose" {
			fmt.Printf("Skipping binary file processing: %s\n", path)
		}
		return
	}

	// Read the file content
	content, err := os.ReadFile(path)
	if err != nil {
		logrus.WithError(err).Errorf("Failed to read file: %s", path)
		return
	}

	// Get file extension
	// ext := filepath.Ext(path)

	// continue if the pattern is not found
	found := false
	for _, pattern := range MagicCommentPatterns {
		if strings.Contains(string(content), pattern) {
			found = true
			break
		}
	}
	if !found {
		return
	}

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
	if verbosity == "verbose" {
		fmt.Println("Sending to AI for analysis...")
	}

	// Get configuration for the LLM
	config := llm.GetConfigFromViper()

	var response string
	var usage types.Usage

	// Use the auto-completion model if appropriate
	if useWeakModel {
		if verbosity == "verbose" {
			fmt.Printf("Using auto-completion model: %v\n", config.WeakModel)
		}
	}
	response, usage = llm.SendMessageAndGetTextWithUsage(ctx, s, query, config, true, types.MessageOpt{
		UseWeakModel: useWeakModel,
		PromptCache:  false,
	})

	// Display the AI response
	fmt.Printf("\n===== AI Analysis for %s =====\n", path)
	fmt.Println(response)
	fmt.Printf("\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Total: %d\033[0m\n",
		usage.InputTokens, usage.OutputTokens, usage.TotalTokens())
	fmt.Println("===============================")
}
