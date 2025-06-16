package browser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSimplifyHTMLComprehensive(t *testing.T) {
	tests := []struct {
		name                string
		html                string
		maxLength           int
		expectedContains    []string
		expectedNotContains []string
		expectTruncated     bool
		description         string
	}{
		{
			name: "basic_interactive_elements",
			html: `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
	<a href="/home">Home Link</a>
	<button onclick="doSomething()">Click Me</button>
	<input type="text" placeholder="Enter name" value="John">
	<textarea placeholder="Comments"></textarea>
	<select>
		<option>Option 1</option>
		<option>Option 2</option>
	</select>
</body>
</html>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <link> Home Link [/home]",
				"[1] <button> Click Me",
				"[2] <input> type=text placeholder='Enter name' value='John'",
				"[3] <textarea> placeholder='Comments'",
				"[4] <select> options: Option 1, Option 2",
			},
			expectedNotContains: []string{
				"<html>", "<head>", "<title>", "onclick=",
			},
			expectTruncated: false,
			description:     "Should extract all interactive elements with proper formatting",
		},
		{
			name: "text_content_extraction",
			html: `<body>
	<div>
		<p>This is a paragraph</p>
		<span>This is a span</span>
		<div>Nested <strong>bold text</strong> here</div>
	</div>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <text> This is a paragraph",
				"[1] <text> This is a span",
				"[2] <text> Nested here", // "here" is part of the same text node
				"[3] <text> bold text",
			},
			expectTruncated: false,
			description:     "Should extract text content from various elements",
		},
		{
			name: "nested_interactive_elements",
			html: `<body>
	<div>
		<a href="/page1">
			<button>Link with button inside</button>
		</a>
		<button>
			<span>Button with text</span>
		</button>
	</div>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <link> Link with button inside [/page1]",
				"[1] <button> Button with text",
			},
			expectTruncated: false,
			description:     "Should handle nested interactive elements correctly",
		},
		{
			name: "form_elements",
			html: `<body>
	<form>
		<input type="email" name="email" placeholder="Email">
		<input type="password" name="pass" value="secret">
		<input type="submit" value="Submit">
		<input type="checkbox" name="agree">
		<input type="radio" name="choice" value="yes">
	</form>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"type=email",
				"name='email'",
				"placeholder='Email'",
				"type=password",
				"name='pass'",
				"type=submit",
				"value='Submit'",
				"type=checkbox",
				"type=radio",
			},
			expectedNotContains: []string{
				"value='secret'", // Password values should not be included
			},
			expectTruncated: false,
			description:     "Should handle various form input types",
		},
		{
			name: "images_with_attributes",
			html: `<body>
	<img src="/image1.jpg" alt="Description of image">
	<img src="/image2.png">
	<img alt="Only alt text">
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <img> alt='Description of image' src='/image1.jpg'",
				"[1] <img> src='/image2.png'",
				"[2] <img> alt='Only alt text'",
			},
			expectTruncated: false,
			description:     "Should extract image elements with alt and src attributes",
		},
		{
			name: "blacklisted_elements",
			html: `<body>
	<script>console.log('test');</script>
	<style>body { color: red; }</style>
	<noscript>JavaScript disabled</noscript>
	<svg><path d="M0 0"/></svg>
	<iframe src="/frame"></iframe>
	<p>Visible content</p>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <text> Visible content",
			},
			expectedNotContains: []string{
				"console.log",
				"color: red",
				"JavaScript disabled",
				"M0 0",
				"iframe",
			},
			expectTruncated: false,
			description:     "Should filter out blacklisted elements",
		},
		{
			name: "empty_elements",
			html: `<body>
	<div></div>
	<p>   </p>
	<button></button>
	<a href="/link"></a>
	<span>Non-empty</span>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <button>",       // Empty button should still be included
				"[1] <link> [/link]", // Empty link with href should be included (no double space)
				"[2] <text> Non-empty",
			},
			expectTruncated: false,
			description:     "Should handle empty elements appropriately",
		},
		{
			name: "truncation_test",
			html: `<body>
	<p>This is a very long text that should be truncated when max length is small</p>
	<p>Another paragraph that won't be included</p>
</body>`,
			maxLength: 50,
			expectedContains: []string{
				"[0] <text> This is a very long text that should be",
			},
			expectTruncated: true,
			description:     "Should truncate content when exceeding max length",
		},
		{
			name: "malformed_html",
			html: `<body>
	<p>Unclosed paragraph
	<div>Unclosed div with <a href="/link">link</a>
	<button>Button</button>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"Unclosed paragraph",
				"Unclosed div with",
				"<link>",
				"<button>",
			},
			expectTruncated: false,
			description:     "Should handle malformed HTML gracefully",
		},
		{
			name: "special_characters",
			html: `<body>
	<p>Text with &lt;special&gt; &amp; characters</p>
	<input type="text" placeholder="Enter &quot;name&quot;">
	<a href="/search?q=test&amp;p=1">Search &amp; Find</a>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"Text with <special> & characters",
				"placeholder='Enter \"name\"'",
				"Search & Find",
			},
			expectTruncated: false,
			description:     "Should handle HTML entities correctly",
		},
		{
			name: "clickable_elements",
			html: `<body>
	<div onclick="handleClick()">Clickable div</div>
	<span role="button">Button role span</span>
	<p>Regular paragraph</p>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <button> Clickable div",
				"[1] <button> Button role span",
				"[2] <text> Regular paragraph",
			},
			expectTruncated: false,
			description:     "Should detect clickable elements by onclick and role attributes",
		},
		{
			name: "whitespace_handling",
			html: `<body>
	<p>
		Text   with    multiple
		
		
		spaces   and newlines
	</p>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <text> Text with multiple",
				"spaces and newlines",
			},
			expectTruncated: false,
			description:     "Should preserve whitespace structure in text content",
		},
		{
			name: "no_body_tag",
			html: `<div>
	<a href="/test">Link without body</a>
	<p>Paragraph content</p>
</div>`,
			maxLength: 1000,
			expectedContains: []string{
				"<link>",
				"Link without body",
				"<text>",
				"Paragraph content",
			},
			expectTruncated: false,
			description:     "Should handle HTML without body tag",
		},
		{
			name: "complex_nested_structure",
			html: `<body>
	<div class="container">
		<header>
			<nav>
				<ul>
					<li><a href="/home">Home</a></li>
					<li><a href="/about">About</a></li>
				</ul>
			</nav>
		</header>
		<main>
			<article>
				<h1>Title</h1>
				<p>Content with <a href="/link">embedded link</a> here.</p>
			</article>
		</main>
	</div>
</body>`,
			maxLength: 1000,
			expectedContains: []string{
				"[0] <link> Home [/home]",
				"[1] <link> About [/about]",
				"[2] <text> Title",
				"[3] <text> Content with here.", // "here." is part of the same text node
				"[4] <link> embedded link [/link]",
			},
			expectTruncated: false,
			description:     "Should handle complex nested HTML structures",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			simplified, truncated := SimplifyHTML(tt.html, tt.maxLength)

			t.Logf("Test: %s", tt.name)
			t.Logf("Description: %s", tt.description)
			t.Logf("Simplified output:\n%s", simplified)
			t.Logf("Truncated: %v", truncated)

			// Check expected content
			for _, expected := range tt.expectedContains {
				assert.Contains(t, simplified, expected,
					"Expected to find '%s' in simplified HTML", expected)
			}

			// Check unexpected content
			for _, notExpected := range tt.expectedNotContains {
				assert.NotContains(t, simplified, notExpected,
					"Did not expect to find '%s' in simplified HTML", notExpected)
			}

			// Check truncation
			assert.Equal(t, tt.expectTruncated, truncated,
				"Truncation status mismatch")

			// Check max length
			assert.LessOrEqual(t, len(simplified), tt.maxLength,
				"Simplified content exceeds max length")
		})
	}
}

func TestBasicSimplifyHTML(t *testing.T) {
	// Test the fallback function directly
	html := `<html>
		<head>
			<script>console.log('test');</script>
			<style>body { color: red; }</style>
		</head>
		<body class="main" style="background: white;">
			<div data-id="123" aria-label="content">
				<p>Test content</p>
			</div>
		</body>
	</html>`

	simplified, truncated := basicSimplifyHTML(html, 1000)

	// Should remove scripts and styles
	assert.NotContains(t, simplified, "console.log")
	assert.NotContains(t, simplified, "color: red")

	// Should remove attributes
	assert.NotContains(t, simplified, "class=")
	assert.NotContains(t, simplified, "style=")
	assert.NotContains(t, simplified, "data-id=")
	assert.NotContains(t, simplified, "aria-label=")

	// Should keep content
	assert.Contains(t, simplified, "Test content")

	// Should not be truncated
	assert.False(t, truncated)
}

func TestCleanupWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "multiple_spaces",
			input:    "Text  with   multiple    spaces",
			expected: "Text with multiple spaces",
		},
		{
			name:     "multiple_newlines",
			input:    "Line1\n\n\n\nLine2",
			expected: "Line1\n\nLine2",
		},
		{
			name:     "mixed_whitespace",
			input:    "  Text\n\n\n  with   spaces  \n\n\n  and newlines  ",
			expected: "Text\n\n with spaces \n\n and newlines",
		},
		{
			name:     "leading_trailing_spaces",
			input:    "   trimmed   ",
			expected: "trimmed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanupWhitespace(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractElement(t *testing.T) {
	// This would require creating goquery selections, which is complex for unit tests
	// The SimplifyHTML tests above provide good coverage of extractElement functionality
}

func TestRemoveTagsWithContent(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		tags     []string
		expected string
	}{
		{
			name:     "single_script_tag",
			html:     `<p>Before</p><script>alert('test');</script><p>After</p>`,
			tags:     []string{"script"},
			expected: `<p>Before</p><p>After</p>`,
		},
		{
			name:     "multiple_tags",
			html:     `<p>Text</p><script>js</script><style>css</style><p>More</p>`,
			tags:     []string{"script", "style"},
			expected: `<p>Text</p><p>More</p>`,
		},
		{
			name:     "nested_content",
			html:     `<div><script><p>Nested</p></script></div>`,
			tags:     []string{"script"},
			expected: `<div></div>`,
		},
		{
			name:     "case_insensitive",
			html:     `<p>Text</p><SCRIPT>code</SCRIPT><p>More</p>`,
			tags:     []string{"script"},
			expected: `<p>Text</p><p>More</p>`,
		},
		{
			name:     "unclosed_tag",
			html:     `<p>Before</p><script>unclosed<p>After</p>`,
			tags:     []string{"script"},
			expected: `<p>Before</p>unclosed<p>After</p>`, // Only removes the opening tag, not the content
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeTagsWithContent(tt.html, tt.tags[0], tt.tags[1:]...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSimplifyHTMLEdgeCases(t *testing.T) {
	t.Run("empty_html", func(t *testing.T) {
		simplified, truncated := SimplifyHTML("", 100)
		assert.Equal(t, "", simplified)
		assert.False(t, truncated)
	})

	t.Run("only_whitespace", func(t *testing.T) {
		simplified, truncated := SimplifyHTML("   \n\n   \t  ", 100)
		assert.Equal(t, "", simplified)
		assert.False(t, truncated)
	})

	t.Run("invalid_html", func(t *testing.T) {
		// Should fallback to basicSimplifyHTML
		simplified, truncated := SimplifyHTML("<<<invalid>>>html", 100)
		assert.NotEmpty(t, simplified)
		assert.False(t, truncated)
	})

	t.Run("zero_max_length", func(t *testing.T) {
		simplified, truncated := SimplifyHTML("<p>Test</p>", 0)
		assert.Equal(t, "", simplified)
		assert.True(t, truncated)
	})

	t.Run("very_large_html", func(t *testing.T) {
		// Create a large HTML string
		var sb strings.Builder
		sb.WriteString("<body>")
		for i := 0; i < 1000; i++ {
			sb.WriteString("<p>Paragraph ")
			sb.WriteString(string(rune(i)))
			sb.WriteString("</p>")
		}
		sb.WriteString("</body>")

		simplified, truncated := SimplifyHTML(sb.String(), 1000)
		assert.LessOrEqual(t, len(simplified), 1000)
		assert.True(t, truncated)
	})
}

func TestManagerCreation(t *testing.T) {
	manager := NewManager()

	assert.NotNil(t, manager)
	assert.Nil(t, manager.ctx)
	assert.Nil(t, manager.cancelCtx)
	assert.False(t, manager.isActive)
}

func TestScreenshotHelpers(t *testing.T) {
	t.Run("create_screenshot_dir", func(t *testing.T) {
		dir, err := CreateScreenshotDir()
		assert.NoError(t, err)
		assert.NotEmpty(t, dir)
		assert.Contains(t, dir, ".kodelet")
		assert.Contains(t, dir, "screenshots")
	})

	t.Run("generate_screenshot_path", func(t *testing.T) {
		tests := []struct {
			format   string
			expected string
		}{
			{"png", ".png"},
			{"jpeg", ".jpeg"},
			{"jpg", ".jpg"},
		}

		for _, tt := range tests {
			t.Run(tt.format, func(t *testing.T) {
				path, err := GenerateScreenshotPath(tt.format)
				assert.NoError(t, err)
				assert.Contains(t, path, tt.expected)
				assert.Contains(t, path, ".kodelet")
				assert.Contains(t, path, "screenshots")
			})
		}
	})
}
