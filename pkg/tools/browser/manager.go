package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	"github.com/jingkaihe/kodelet/pkg/logger"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"golang.org/x/net/html"
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

		// Realistic User Agent - Chrome on Windows 11
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),

		// Realistic viewport dimensions - Full HD (most common desktop resolution)
		chromedp.WindowSize(1920, 1080),

		// Additional flags for more realistic behavior
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", false),   // Allow extensions for realism
		chromedp.Flag("disable-plugins", false),      // Allow plugins for realism
		chromedp.Flag("disable-images", false),       // Allow images for realistic behavior
		chromedp.Flag("disable-javascript", false),   // Allow JS for realistic behavior
		chromedp.Flag("disable-web-security", false), // Keep security enabled

		// Hide automation indicators
		chromedp.Flag("excludeSwitches", "enable-automation"),
		chromedp.Flag("useAutomationExtension", false),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),

		// Language and locale settings
		chromedp.Flag("lang", "en-US,en"),
		chromedp.Flag("accept-lang", "en-US,en;q=0.9"),

		// Timezone and location
		chromedp.Flag("timezone", "America/New_York"),

		// Memory and performance optimizations
		chromedp.Flag("memory-pressure-off", true),
		chromedp.Flag("max_old_space_size", "4096"),

		// Additional realism flags
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceLogging"),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("metrics-recording-only", true),
	}

	allocCtx, _ := chromedp.NewExecAllocator(ctx, opts...)
	m.allocCtx = allocCtx

	browserCtx, cancelCtx := chromedp.NewContext(allocCtx)
	m.ctx = browserCtx
	m.cancelCtx = cancelCtx
	m.isActive = true

	// Configure browser for realistic behavior
	err := m.setupRealisticBrowserBehavior(m.ctx)
	if err != nil {
		m.Stop()
		return fmt.Errorf("failed to configure browser: %w", err)
	}

	// Test browser startup
	var title string
	err = chromedp.Run(m.ctx, chromedp.Navigate("about:blank"), chromedp.Title(&title))
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

// setupRealisticBrowserBehavior configures the browser to appear more human-like
func (m *Manager) setupRealisticBrowserBehavior(ctx context.Context) error {
	// Hide automation indicators and set realistic properties
	err := chromedp.Run(ctx,
		// Remove webdriver property that can detect automation
		chromedp.Evaluate(`
			Object.defineProperty(navigator, 'webdriver', {
				get: () => undefined,
			});
		`, nil),

		// Set realistic screen properties
		chromedp.Evaluate(`
			Object.defineProperty(screen, 'availWidth', {
				get: () => 1920,
			});
			Object.defineProperty(screen, 'availHeight', {
				get: () => 1040,
			});
			Object.defineProperty(screen, 'width', {
				get: () => 1920,
			});
			Object.defineProperty(screen, 'height', {
				get: () => 1080,
			});
		`, nil),

		// Set realistic language and platform properties
		chromedp.Evaluate(`
			Object.defineProperty(navigator, 'language', {
				get: () => 'en-US',
			});
			Object.defineProperty(navigator, 'languages', {
				get: () => ['en-US', 'en'],
			});
			Object.defineProperty(navigator, 'platform', {
				get: () => 'Win32',
			});
		`, nil),

		// Set realistic plugin and mime type arrays (empty but present)
		chromedp.Evaluate(`
			Object.defineProperty(navigator, 'plugins', {
				get: () => [1, 2, 3, 4, 5],
			});
			Object.defineProperty(navigator, 'mimeTypes', {
				get: () => [1, 2, 3, 4],
			});
		`, nil),

		// Set realistic timezone
		chromedp.Evaluate(`
			try {
				Intl.DateTimeFormat().resolvedOptions().timeZone = 'America/New_York';
			} catch (e) {}
		`, nil),

		// Set realistic connection information
		chromedp.Evaluate(`
			Object.defineProperty(navigator, 'connection', {
				get: () => ({
					effectiveType: '4g',
					rtt: 50,
					downlink: 10,
					onchange: null
				}),
			});
		`, nil),

		// Set realistic hardware concurrency
		chromedp.Evaluate(`
			Object.defineProperty(navigator, 'hardwareConcurrency', {
				get: () => 8,
			});
		`, nil),

		// Set realistic device memory
		chromedp.Evaluate(`
			Object.defineProperty(navigator, 'deviceMemory', {
				get: () => 8,
			});
		`, nil),
	)

	return err
}

// SetRealisticHeaders sets realistic HTTP headers for browser requests
func SetRealisticHeaders(ctx context.Context) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return chromedp.Run(ctx,
			// Set realistic Accept headers
			chromedp.Evaluate(`
				// Override fetch to add realistic headers
				const originalFetch = window.fetch;
				window.fetch = function(...args) {
					if (args.length > 1 && args[1] && args[1].headers) {
						args[1].headers = {
							...args[1].headers,
							'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8',
							'Accept-Language': 'en-US,en;q=0.9',
							'Accept-Encoding': 'gzip, deflate, br',
							'Cache-Control': 'no-cache',
							'Pragma': 'no-cache',
							'Sec-Fetch-Dest': 'document',
							'Sec-Fetch-Mode': 'navigate',
							'Sec-Fetch-Site': 'none',
							'Sec-Fetch-User': '?1',
							'Upgrade-Insecure-Requests': '1'
						};
					}
					return originalFetch.apply(this, args);
				};
			`, nil),
		)
	})
}

// SimplifyHTML removes unnecessary attributes and elements for LLM analysis
func SimplifyHTML(htmlContent string, maxLength int) (string, bool) {
	// Parse HTML to extract meaningful content similar to natbot.py
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		// Fallback to basic simplification if parsing fails
		return basicSimplifyHTML(htmlContent, maxLength)
	}

	// Elements to completely skip (similar to natbot's black_listed_elements)
	blacklistedElements := map[string]bool{
		"script": true, "style": true, "meta": true, "link": true,
		"noscript": true, "svg": true, "path": true, "head": true,
		"iframe": true, "object": true, "embed": true, "param": true,
		"source": true, "track": true, "br": true,
	}

	var elements []string
	elementIndex := 0
	processedNodes := make(map[*goquery.Selection]bool)

	// Process only leaf nodes and interactive elements to avoid duplication
	var processNode func(*goquery.Selection)
	processNode = func(s *goquery.Selection) {
		// Skip if already processed
		if processedNodes[s] {
			return
		}
		processedNodes[s] = true

		tagName := goquery.NodeName(s)

		// Skip blacklisted elements
		if blacklistedElements[tagName] {
			return
		}

		// Check if this is an interactive element or has direct text
		isInteractive := tagName == "a" || tagName == "button" || tagName == "input" ||
			tagName == "select" || tagName == "textarea" || tagName == "img"

		// Check if element has direct text content (not in child elements)
		hasDirectText := false
		ownText := s.Contents().FilterFunction(func(i int, s *goquery.Selection) bool {
			if s.Nodes != nil && len(s.Nodes) > 0 {
				node := s.Nodes[0]
				// Check if it's a text node
				return node.Type == html.TextNode
			}
			return false
		}).Text()

		if strings.TrimSpace(ownText) != "" {
			hasDirectText = true
		}

		// Process interactive elements or elements with direct text
		if isInteractive || hasDirectText {
			elementStr := extractElement(s, tagName, elementIndex)
			if elementStr != "" {
				elements = append(elements, elementStr)
				elementIndex++
			}

			// Don't process children of interactive elements
			if isInteractive {
				return
			}
		}

		// Process children
		s.Children().Each(func(i int, child *goquery.Selection) {
			processNode(child)
		})
	}

	// Start processing from body or document root
	body := doc.Find("body")
	if body.Length() > 0 {
		processNode(body)
	} else {
		processNode(doc.Selection)
	}

	// Join all elements
	result := strings.Join(elements, "\n")

	// Clean up extra whitespace
	result = cleanupWhitespace(result)

	truncated := false
	if len(result) > maxLength {
		result = result[:maxLength]
		truncated = true
	}

	return result, truncated
}

// extractElement extracts meaningful content from an element, similar to natbot's approach
func extractElement(s *goquery.Selection, tagName string, index int) string {
	// Get element's own text content (not including children)
	text := ""
	if tagName != "input" && tagName != "img" {
		// For most elements, get the direct text content
		ownText := s.Contents().FilterFunction(func(i int, sel *goquery.Selection) bool {
			if sel.Nodes != nil && len(sel.Nodes) > 0 {
				return sel.Nodes[0].Type == html.TextNode
			}
			return false
		}).Text()
		text = strings.TrimSpace(ownText)

		// For some elements like buttons and links, we want all text content
		if tagName == "a" || tagName == "button" || text == "" {
			text = strings.TrimSpace(s.Text())
		}
	}

	// Check if element has click handlers or is interactive
	_, hasOnClick := s.Attr("onclick")

	// Convert tag names similar to natbot's convert_name function
	elementType := ""
	switch tagName {
	case "a":
		elementType = "link"
		if href, exists := s.Attr("href"); exists {
			text = fmt.Sprintf("%s [%s]", text, href)
		}
	case "button":
		elementType = "button"
	case "input":
		elementType = "input"
		inputType, _ := s.Attr("type")
		placeholder, _ := s.Attr("placeholder")
		value, _ := s.Attr("value")
		name, _ := s.Attr("name")

		parts := []string{fmt.Sprintf("type=%s", inputType)}
		if placeholder != "" {
			parts = append(parts, fmt.Sprintf("placeholder='%s'", placeholder))
		}
		if value != "" && inputType != "password" {
			parts = append(parts, fmt.Sprintf("value='%s'", value))
		}
		if name != "" {
			parts = append(parts, fmt.Sprintf("name='%s'", name))
		}
		text = strings.Join(parts, " ")
	case "img":
		elementType = "img"
		alt, _ := s.Attr("alt")
		src, _ := s.Attr("src")
		if alt != "" {
			text = fmt.Sprintf("alt='%s'", alt)
		}
		if src != "" {
			text += fmt.Sprintf(" src='%s'", src)
		}
	case "select":
		elementType = "select"
		options := []string{}
		s.Find("option").Each(func(i int, opt *goquery.Selection) {
			optText := strings.TrimSpace(opt.Text())
			if optText != "" {
				options = append(options, optText)
			}
		})
		if len(options) > 0 {
			text = fmt.Sprintf("options: %s", strings.Join(options, ", "))
		}
	case "textarea":
		elementType = "textarea"
		placeholder, _ := s.Attr("placeholder")
		if placeholder != "" {
			text = fmt.Sprintf("placeholder='%s'", placeholder)
		}
	default:
		// For other elements, check if they're clickable
		if hasOnClick || s.Is("[role='button']") {
			elementType = "button"
		} else if text != "" {
			elementType = "text"
		}
	}

	// Skip empty elements
	if elementType == "" || (text == "" && elementType == "text") {
		return ""
	}

	// Format output similar to natbot
	return fmt.Sprintf("[%d] <%s> %s", index, elementType, text)
}

// cleanupWhitespace removes excessive whitespace and formats the output
func cleanupWhitespace(s string) string {
	// Remove multiple consecutive newlines
	re := regexp.MustCompile(`\n{3,}`)
	s = re.ReplaceAllString(s, "\n\n")

	// Remove multiple consecutive spaces
	re = regexp.MustCompile(` {2,}`)
	s = re.ReplaceAllString(s, " ")

	// Trim leading and trailing whitespace
	s = strings.TrimSpace(s)

	return s
}

// basicSimplifyHTML is a fallback for when goquery parsing fails
func basicSimplifyHTML(htmlContent string, maxLength int) (string, bool) {
	// Remove script and style tags with their content
	result := removeTagsWithContent(htmlContent, "script", "style")

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
		re := regexp.MustCompile(attr)
		result = re.ReplaceAllString(result, "")
	}

	// Clean up extra whitespace
	result = cleanupWhitespace(result)

	truncated := false
	if len(result) > maxLength {
		result = result[:maxLength]
		truncated = true
	}

	return result, truncated
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
