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

	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Element represents a crawled element with its position and metadata
type Element struct {
	NodeIndex     int      `json:"node_index"`
	BackendNodeID int      `json:"backend_node_id"`
	NodeName      string   `json:"node_name"`
	NodeValue     string   `json:"node_value"`
	NodeMeta      []string `json:"node_meta"`
	IsClickable   bool     `json:"is_clickable"`
	OriginX       int      `json:"origin_x"`
	OriginY       int      `json:"origin_y"`
	CenterX       int      `json:"center_x"`
	CenterY       int      `json:"center_y"`
}

// Manager handles browser context and lifecycle
type Manager struct {
	ctx           context.Context
	cancelCtx     context.CancelFunc
	allocCtx      context.Context
	mutex         sync.Mutex
	isActive      bool
	elementBuffer map[int]*Element
	elementMutex  sync.RWMutex
}

// Ensure Manager implements the BrowserManager interface
var _ tooltypes.BrowserManager = (*Manager)(nil)

// NewManager creates a new browser manager instance
func NewManager() *Manager {
	return &Manager{
		elementBuffer: make(map[int]*Element),
	}
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
		return errors.Wrap(err, "failed to configure browser")
	}

	// Test browser startup
	var title string
	err = chromedp.Run(m.ctx, chromedp.Navigate("about:blank"), chromedp.Title(&title))
	if err != nil {
		m.Stop()
		return errors.Wrap(err, "failed to start browser")
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

// Crawl extracts and simplifies page content using DOM snapshot approach
func (m *Manager) Crawl(ctx context.Context, maxLength int) (string, bool, error) {
	if m.ctx == nil {
		return "", false, errors.New("browser context not available")
	}

	// Get viewport information
	var viewport struct {
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
	}

	err := chromedp.Run(m.ctx,
		chromedp.Evaluate(`({
			width: window.innerWidth,
			height: window.innerHeight,
			x: window.scrollX,
			y: window.scrollY
		})`, &viewport),
	)
	if err != nil {
		return "", false, errors.Wrap(err, "failed to get viewport info")
	}

	// Extract page elements for interaction
	var result interface{}
	err = chromedp.Run(m.ctx,
		chromedp.Evaluate(`(() => {
				const elements = [];
				const elementBuffer = {};
				let idCounter = 0;

				function isInViewport(rect) {
					return rect.left < window.innerWidth + window.scrollX &&
						   rect.right >= window.scrollX &&
						   rect.top < window.innerHeight + window.scrollY &&
						   rect.bottom >= window.scrollY;
				}

				function convertNodeName(nodeName, hasClickHandler) {
					if (nodeName === 'A') return 'link';
					if (nodeName === 'INPUT') return 'input';
					if (nodeName === 'IMG') return 'img';
					if (nodeName === 'BUTTON' || hasClickHandler) return 'button';
					if (nodeName === 'SELECT') return 'select';
					if (nodeName === 'TEXTAREA') return 'textarea';
					return 'text';
				}

				function getTextWithoutBlacklisted(element) {
					// Get only direct text and text from non-blacklisted children
					const blacklisted = ['SCRIPT', 'STYLE', 'NOSCRIPT'];
					let text = '';

					for (const node of element.childNodes) {
						if (node.nodeType === Node.TEXT_NODE) {
							text += node.textContent;
						} else if (node.nodeType === Node.ELEMENT_NODE && !blacklisted.includes(node.nodeName)) {
							text += getTextWithoutBlacklisted(node);
						}
					}

					return text.trim();
				}

				function processElement(element) {
					const nodeName = element.nodeName;
					const rect = element.getBoundingClientRect();

					// Skip if not in viewport
					if (!isInViewport(rect)) return;

					// Skip blacklisted elements
					const blacklisted = ['HTML', 'HEAD', 'TITLE', 'META', 'IFRAME',
										'BODY', 'SCRIPT', 'STYLE', 'PATH', 'SVG', 'BR', 'NOSCRIPT'];
					if (blacklisted.includes(nodeName)) return;

					const isClickable = element.onclick !== null ||
									   element.hasAttribute('onclick') ||
									   ['A', 'BUTTON', 'INPUT', 'SELECT', 'TEXTAREA'].includes(nodeName);

					let innerText = '';
					let meta = [];

					// Extract text content
					if (nodeName === 'INPUT') {
						const type = element.type || 'text';
						const placeholder = element.placeholder || '';
						const value = element.value || '';
						const name = element.name || '';

						meta.push('type=' + type);
						if (placeholder) meta.push('placeholder="' + placeholder + '"');
						if (value && type !== 'password') meta.push('value="' + value + '"');
						if (name) meta.push('name="' + name + '"');
						innerText = meta.join(' ');
					} else if (nodeName === 'TEXTAREA') {
						const value = element.value || '';
						const placeholder = element.placeholder || '';
						const name = element.name || '';
						const rows = element.rows || '';
						const cols = element.cols || '';

						if (value) meta.push('value="' + value + '"');
						if (placeholder) meta.push('placeholder="' + placeholder + '"');
						if (name) meta.push('name="' + name + '"');
						if (rows) meta.push('rows="' + rows + '"');
						if (cols) meta.push('cols="' + cols + '"');
						innerText = meta.join(' ');
					} else if (nodeName === 'IMG') {
						const alt = element.alt || '';
						const src = element.src || '';
						if (alt) meta.push('alt="' + alt + '"');
						if (src) meta.push('src="' + src + '"');
						innerText = meta.join(' ');
					} else if (nodeName === 'A') {
						innerText = getTextWithoutBlacklisted(element);
						const href = element.href || '';
						if (href) innerText += ' [' + href + ']';
					} else if (nodeName === 'SELECT') {
						const options = Array.from(element.options).map(opt => opt.text).join(', ');
						innerText = 'options: ' + options;
					} else {
						innerText = getTextWithoutBlacklisted(element);
					}

					// Skip empty elements unless they're interactive
					if (!innerText && !isClickable) return;

					const convertedNodeName = convertNodeName(nodeName, isClickable);

					// Store element info for coordinate mapping
					elementBuffer[idCounter] = {
						center_x: rect.left + rect.width / 2,
						center_y: rect.top + rect.height / 2,
						origin_x: rect.left,
						origin_y: rect.top,
						width: rect.width,
						height: rect.height
					};

					if (innerText) {
						elements.push('<' + convertedNodeName + ' id=' + idCounter + '>' + innerText + '</' + convertedNodeName + '>');
					} else {
						elements.push('<' + convertedNodeName + ' id=' + idCounter + '/>');
					}

					idCounter++;
				}

				// Process all elements in document
				const allElements = document.querySelectorAll('*');
				for (let i = 0; i < allElements.length; i++) {
					processElement(allElements[i]);
				}

				return {
					elements: elements,
					elementBuffer: elementBuffer
				};
			})()`, &result),
	)

	if err != nil {
		return "", false, errors.Wrap(err, "failed to capture DOM snapshot")
	}

	// Parse the result directly since it's already structured data from JavaScript
	crawlResult, ok := result.(map[string]interface{})
	if !ok {
		return "", false, errors.New("unexpected result format from JavaScript")
	}

	elements, ok := crawlResult["elements"].([]interface{})
	if !ok {
		return "", false, errors.New("unexpected elements format")
	}

	elementBuffer, ok := crawlResult["elementBuffer"].(map[string]interface{})
	if !ok {
		return "", false, errors.New("unexpected elementBuffer format")
	}
	// Update element buffer for coordinate mapping
	m.elementMutex.Lock()
	m.elementBuffer = make(map[int]*Element)
	for idStr, elementDataInterface := range elementBuffer {
		var id int
		fmt.Sscanf(idStr, "%d", &id)

		elementData, ok := elementDataInterface.(map[string]interface{})
		if !ok {
			continue // skip invalid entries
		}

		centerX, _ := elementData["center_x"].(float64)
		centerY, _ := elementData["center_y"].(float64)
		originX, _ := elementData["origin_x"].(float64)
		originY, _ := elementData["origin_y"].(float64)

		m.elementBuffer[id] = &Element{
			CenterX: int(centerX),
			CenterY: int(centerY),
			OriginX: int(originX),
			OriginY: int(originY),
		}
	}
	m.elementMutex.Unlock()

	// Join all elements (convert interface{} slice to string slice)
	var elementStrings []string
	for _, elem := range elements {
		if str, ok := elem.(string); ok {
			elementStrings = append(elementStrings, str)
		}
	}
	result_str := strings.Join(elementStrings, "\n")

	// Clean up extra whitespace
	result_str = cleanupWhitespace(result_str)

	truncated := false
	if len(result_str) > maxLength {
		result_str = result_str[:maxLength]
		truncated = true
	}

	return result_str, truncated, nil
}

// GetElement returns element data by ID for coordinate mapping
func (m *Manager) GetElement(id int) (*Element, bool) {
	m.elementMutex.RLock()
	defer m.elementMutex.RUnlock()

	element, exists := m.elementBuffer[id]
	return element, exists
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

// CreateScreenshotDir ensures the screenshots directory exists
func CreateScreenshotDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get home directory")
	}

	screenshotDir := filepath.Join(homeDir, ".kodelet", "screenshots")

	if err := os.MkdirAll(screenshotDir, 0755); err != nil {
		return "", errors.Wrap(err, "failed to create screenshots directory")
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

// WaitForCondition waits for page load condition
func WaitForCondition(ctx context.Context, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return chromedp.Run(timeoutCtx, chromedp.WaitReady("body"))
}
