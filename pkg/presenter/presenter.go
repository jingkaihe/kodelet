// Package presenter provides consistent CLI output functionality for user-facing messages,
// including success, error, warning, and informational output with color support and quiet mode.
package presenter

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// UsageStats represents token usage and cost information
type UsageStats struct {
	InputTokens          int64
	OutputTokens         int64
	CacheWriteTokens     int64
	CacheReadTokens      int64
	InputCost            float64
	OutputCost           float64
	CacheWriteCost       float64
	CacheReadCost        float64
	CurrentContextWindow int
	MaxContextWindow     int
}

// Presenter defines the interface for consistent CLI output
type Presenter interface {
	Error(err error, context string)
	Success(message string)
	Warning(message string)
	Info(message string)
	Section(title string)
	Prompt(question string, options ...string) string
	Stats(usage *UsageStats)
	Separator()
	SetQuiet(quiet bool)
	IsQuiet() bool
}

// TerminalPresenter implements Presenter for terminal output
type TerminalPresenter struct {
	output      io.Writer
	errorOutput io.Writer
	colorMode   ColorMode
	quiet       bool
}

// ColorMode represents different color output modes
type ColorMode int

const (
	// ColorAuto automatically detects whether to use colored output based on terminal capabilities
	ColorAuto ColorMode = iota
	// ColorAlways forces colored output regardless of terminal capabilities
	ColorAlways
	// ColorNever disables colored output regardless of terminal capabilities
	ColorNever
)

// New creates a new TerminalPresenter with default settings
func New() *TerminalPresenter {
	return NewWithOptions(os.Stdout, os.Stderr, detectColorMode())
}

// NewWithOptions creates a TerminalPresenter with custom settings
func NewWithOptions(output, errorOutput io.Writer, colorMode ColorMode) *TerminalPresenter {
	presenter := &TerminalPresenter{
		output:      output,
		errorOutput: errorOutput,
		colorMode:   colorMode,
		quiet:       false,
	}

	// Configure color package based on mode
	switch colorMode {
	case ColorAlways:
		color.NoColor = false
	case ColorNever:
		color.NoColor = true
	case ColorAuto:
		// Let color package auto-detect
	}

	return presenter
}

// detectColorMode determines the appropriate color mode based on environment
func detectColorMode() ColorMode {
	// Check explicit environment variables
	if os.Getenv("NO_COLOR") != "" {
		return ColorNever
	}

	kodeletColor := os.Getenv("KODELET_COLOR")
	switch kodeletColor {
	case "always", "force":
		return ColorAlways
	case "never", "off":
		return ColorNever
	case "auto", "":
		return ColorAuto
	default:
		return ColorAuto
	}
}

// Error displays an error message to stderr
func (p *TerminalPresenter) Error(err error, context string) {
	if err == nil {
		return
	}

	errorColor := color.New(color.FgRed, color.Bold)
	if context != "" {
		errorColor.Fprintf(p.errorOutput, "[ERROR] %s: %v\n", context, err)
	} else {
		errorColor.Fprintf(p.errorOutput, "[ERROR] %v\n", err)
	}
}

// Success displays a success message
func (p *TerminalPresenter) Success(message string) {
	if p.quiet {
		return
	}

	successColor := color.New(color.FgGreen, color.Bold)
	successColor.Fprintf(p.output, "✓ %s\n", message)
}

// Warning displays a warning message
func (p *TerminalPresenter) Warning(message string) {
	if p.quiet {
		return
	}

	warningColor := color.New(color.FgYellow, color.Bold)
	warningColor.Fprintf(p.output, "⚠ %s\n", message)
}

// Info displays an informational message
func (p *TerminalPresenter) Info(message string) {
	if p.quiet {
		return
	}

	fmt.Fprintf(p.output, "%s\n", message)
}

// Section displays a section header with consistent formatting
func (p *TerminalPresenter) Section(title string) {
	if p.quiet {
		return
	}

	headerColor := color.New(color.Bold)
	separator := strings.Repeat("-", len(title))

	headerColor.Fprintf(p.output, "%s\n", title)
	headerColor.Fprintf(p.output, "%s\n", separator)
}

// Prompt displays a prompt and reads user input
func (p *TerminalPresenter) Prompt(question string, options ...string) string {
	promptColor := color.New(color.FgCyan)

	if len(options) > 0 {
		optionsStr := strings.Join(options, "/")
		promptColor.Fprintf(p.output, "%s [%s]: ", question, optionsStr)
	} else {
		promptColor.Fprintf(p.output, "%s: ", question)
	}

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}

	return strings.TrimSpace(response)
}

// Stats displays usage statistics in a consistent format
func (p *TerminalPresenter) Stats(usage *UsageStats) {
	if p.quiet || usage == nil {
		return
	}

	statsColor := color.New(color.FgCyan, color.Bold)

	// Token usage stats
	totalTokens := usage.InputTokens + usage.OutputTokens + usage.CacheWriteTokens + usage.CacheReadTokens
	statsColor.Fprintf(p.output, "[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\n",
		usage.InputTokens, usage.OutputTokens, usage.CacheWriteTokens, usage.CacheReadTokens, totalTokens)

	// Context window stats
	if usage.MaxContextWindow > 0 {
		percentage := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow) * 100
		statsColor.Fprintf(p.output, "[Context Window] Current: %d | Max: %d | Usage: %.1f%%\n",
			usage.CurrentContextWindow, usage.MaxContextWindow, percentage)
	}

	// Cost stats
	totalCost := usage.InputCost + usage.OutputCost + usage.CacheWriteCost + usage.CacheReadCost
	statsColor.Fprintf(p.output, "[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\n",
		usage.InputCost, usage.OutputCost, usage.CacheWriteCost, usage.CacheReadCost, totalCost)
}

// Separator displays a visual separator
func (p *TerminalPresenter) Separator() {
	if p.quiet {
		return
	}

	separatorColor := color.New(color.Faint)
	separatorColor.Fprintf(p.output, "%s\n", strings.Repeat("-", 60))
}

// SetQuiet enables or disables quiet mode
func (p *TerminalPresenter) SetQuiet(quiet bool) {
	p.quiet = quiet
}

// IsQuiet returns whether quiet mode is enabled
func (p *TerminalPresenter) IsQuiet() bool {
	return p.quiet
}

// ConvertUsageStats converts llmtypes.Usage to presenter.UsageStats
func ConvertUsageStats(stats *llmtypes.Usage) *UsageStats {
	if stats == nil {
		return nil
	}

	return &UsageStats{
		InputTokens:          int64(stats.InputTokens),
		OutputTokens:         int64(stats.OutputTokens),
		CacheWriteTokens:     int64(stats.CacheCreationInputTokens),
		CacheReadTokens:      int64(stats.CacheReadInputTokens),
		InputCost:            stats.InputCost,
		OutputCost:           stats.OutputCost,
		CacheWriteCost:       stats.CacheCreationCost,
		CacheReadCost:        stats.CacheReadCost,
		CurrentContextWindow: stats.CurrentContextWindow,
		MaxContextWindow:     stats.MaxContextWindow,
	}
}

// Global presenter instance for convenience
var defaultPresenter = New()

// Error displays an error message using the default presenter instance.
func Error(err error, context string) {
	defaultPresenter.Error(err, context)
}

// Success displays a success message using the default presenter instance.
func Success(message string) {
	defaultPresenter.Success(message)
}

// Warning displays a warning message using the default presenter instance.
func Warning(message string) {
	defaultPresenter.Warning(message)
}

// Info displays an informational message using the default presenter instance.
func Info(message string) {
	defaultPresenter.Info(message)
}

// Section displays a section header using the default presenter instance.
func Section(title string) {
	defaultPresenter.Section(title)
}

// Prompt displays a prompt and reads user input using the default presenter instance.
func Prompt(question string, options ...string) string {
	return defaultPresenter.Prompt(question, options...)
}

// Stats displays usage statistics using the default presenter instance.
func Stats(usage *UsageStats) {
	defaultPresenter.Stats(usage)
}

// Separator displays a visual separator using the default presenter instance.
func Separator() {
	defaultPresenter.Separator()
}

// SetQuiet enables or disables quiet mode for the default presenter instance.
func SetQuiet(quiet bool) {
	defaultPresenter.SetQuiet(quiet)
}

// IsQuiet returns whether quiet mode is enabled for the default presenter instance.
func IsQuiet() bool {
	return defaultPresenter.IsQuiet()
}
