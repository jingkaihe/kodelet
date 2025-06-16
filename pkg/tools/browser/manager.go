package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	"github.com/jingkaihe/kodelet/pkg/logger"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Manager handles browser context and lifecycle
type Manager struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
	allocCtx  context.Context
	mutex     sync.Mutex
	isActive  bool
}

// Ensure Manager implements the BrowserManager interface
var _ tooltypes.BrowserManager = (*Manager)(nil)

// NewManager creates a new browser manager instance
func NewManager() *Manager {
	return &Manager{}
}

// GetManagerFromState retrieves or creates a browser manager from the tool state
func GetManagerFromState(state tooltypes.State) tooltypes.BrowserManager {
	if manager := state.GetBrowserManager(); manager != nil {
		return manager
	}

	// Create a new manager and store it in the state
	manager := NewManager()
	state.SetBrowserManager(manager)
	return manager
}

// Start initializes the browser context
func (m *Manager) Start(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.isActive {
		return nil
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Headless,
		chromedp.UserAgent("Kodelet Browser Agent/1.0"),
	}

	allocCtx, _ := chromedp.NewExecAllocator(ctx, opts...)
	m.allocCtx = allocCtx

	browserCtx, cancelCtx := chromedp.NewContext(allocCtx)
	m.ctx = browserCtx
	m.cancelCtx = cancelCtx
	m.isActive = true

	// Test browser startup
	var title string
	err := chromedp.Run(m.ctx, chromedp.Navigate("about:blank"), chromedp.Title(&title))
	if err != nil {
		m.Stop()
		return fmt.Errorf("failed to start browser: %w", err)
	}

	logger.G(ctx).Info("Browser manager started successfully")
	return nil
}

// Stop shuts down the browser context
func (m *Manager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.isActive {
		return
	}

	if m.cancelCtx != nil {
		m.cancelCtx()
	}

	m.isActive = false
	m.ctx = nil
	m.cancelCtx = nil
	m.allocCtx = nil
}

// GetContext returns the browser context
func (m *Manager) GetContext() context.Context {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	return m.ctx
}

// IsActive returns whether the browser is active
func (m *Manager) IsActive() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	return m.isActive
}

// EnsureActive ensures the browser is running
func (m *Manager) EnsureActive(ctx context.Context) error {
	if !m.IsActive() {
		return m.Start(ctx)
	}
	return nil
}

// SimplifyHTML removes unnecessary attributes and elements for LLM analysis
func SimplifyHTML(html string, maxLength int) (string, bool) {
	// Remove script and style tags with their content
	html = removeTagsWithContent(html, "script", "style")

	// Remove common attributes that aren't useful for analysis
	attributesToRemove := []string{
		`class="[^"]*"`,
		`style="[^"]*"`,
		`data-[^=]*="[^"]*"`,
		`aria-[^=]*="[^"]*"`,
		`role="[^"]*"`,
		`tabindex="[^"]*"`,
		`autocomplete="[^"]*"`,
		`spellcheck="[^"]*"`,
	}

	for _, attr := range attributesToRemove {
		html = strings.ReplaceAll(html, attr, "")
	}

	// Clean up extra whitespace
	html = strings.ReplaceAll(html, "\n\n", "\n")
	html = strings.ReplaceAll(html, "  ", " ")
	html = strings.TrimSpace(html)

	truncated := false
	if len(html) > maxLength {
		html = html[:maxLength]
		truncated = true
	}

	return html, truncated
}

// removeTagsWithContent removes HTML tags and their content
func removeTagsWithContent(html, tagName string, additionalTags ...string) string {
	tags := append([]string{tagName}, additionalTags...)

	for _, tag := range tags {
		// Remove opening tag, content, and closing tag
		startTag := fmt.Sprintf("<%s", tag)
		endTag := fmt.Sprintf("</%s>", tag)

		for {
			start := strings.Index(strings.ToLower(html), strings.ToLower(startTag))
			if start == -1 {
				break
			}

			// Find the end of the opening tag
			tagEnd := strings.Index(html[start:], ">")
			if tagEnd == -1 {
				break
			}
			tagEnd += start + 1

			// Find the closing tag
			end := strings.Index(strings.ToLower(html[tagEnd:]), strings.ToLower(endTag))
			if end == -1 {
				// No closing tag found, just remove the opening tag
				html = html[:start] + html[tagEnd:]
				continue
			}
			end += tagEnd + len(endTag)

			// Remove the entire tag and its content
			html = html[:start] + html[end:]
		}
	}

	return html
}

// CreateScreenshotDir ensures the screenshots directory exists
func CreateScreenshotDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	screenshotDir := filepath.Join(homeDir, ".kodelet", "screenshots")

	if err := os.MkdirAll(screenshotDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create screenshots directory: %w", err)
	}

	return screenshotDir, nil
}

// GenerateScreenshotPath generates a unique screenshot file path
func GenerateScreenshotPath(format string) (string, error) {
	screenshotDir, err := CreateScreenshotDir()
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%s.%s", uuid.New().String(), format)
	return filepath.Join(screenshotDir, filename), nil
}

// WaitForCondition waits for various page conditions
func WaitForCondition(ctx context.Context, condition string, selector string, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch condition {
	case "page_load":
		return chromedp.Run(timeoutCtx, chromedp.WaitReady("body"))
	case "element_visible":
		if selector == "" {
			return fmt.Errorf("selector required for element_visible condition")
		}
		return chromedp.Run(timeoutCtx, chromedp.WaitVisible(selector))
	case "element_hidden":
		if selector == "" {
			return fmt.Errorf("selector required for element_hidden condition")
		}
		return chromedp.Run(timeoutCtx, chromedp.WaitNotPresent(selector))
	default:
		return fmt.Errorf("unknown condition: %s", condition)
	}
}
