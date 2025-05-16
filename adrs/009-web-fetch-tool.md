# ADR 009: WebFetch Tool Implementation

## Status
Proposed

## Context
Currently, Kodelet operates primarily within the local filesystem context and doesn't have the ability to directly access and process web content. Users often need to retrieve information from web pages to provide context for their tasks or to extract specific data for analysis. This requires them to manually fetch web content and paste it into the conversation, which is inefficient and disruptive to workflow.

Common use cases include:
- Retrieving documentation from public websites
- Analyzing public API specifications
- Extracting specific information from web content
- Understanding context from online resources

Adding a web fetch capability would enhance Kodelet's utility for these scenarios while maintaining a seamless user experience.

## Decision
We will implement a new `web_fetch` tool that allows Kodelet to retrieve and process web content. The tool will:

1. Fetch content from a specified URL via HTTP GET requests
2. Follow redirects as long as the domain remains unchanged (for security)
3. Convert HTML content to Markdown for better readability
4. Use LLM processing to extract specifically requested information

### Implementation Details

1. Create a new `WebFetchTool` structure in the `pkg/tools` package:
   ```go
   // pkg/tools/web_fetch.go
   type WebFetchTool struct{}

   type WebFetchInput struct {
       URL    string `json:"url" jsonschema:"description=The URL to fetch content from"`
       Prompt string `json:"prompt" jsonschema:"description=Information to extract from the content"`
   }
   ```

2. Implement the Tool interface methods:
   ```go
   func (t *WebFetchTool) Name() string {
       return "web_fetch"
   }

   func (t *WebFetchTool) GenerateSchema() *jsonschema.Schema {
       return GenerateSchema[WebFetchInput]()
   }

   func (t *WebFetchTool) Description() string {
       return `Fetches content from a web URL and extracts specific information.

   ## Input
   - url: The URL to fetch content from
   - prompt: Information that should be extracted from the content

   ## Behavior
   - Makes an HTTP GET request to the specified URL
   - Follows redirects as long as the domain does not change
   - Converts HTML content to Markdown for better readability
   - Uses AI to extract the requested information from the content

   ## Common Use Cases
   * Retrieving specific information from documentation websites
   * Extracting data from public web pages
   * Analyzing content from online resources
   * Providing context from web-based reference material

   ## Important Notes
   1. Only public URLs that don't require authentication can be accessed
   2. For security reasons, redirects are only followed within the same domain
   3. Binary content types are not supported`
   }
   ```

3. Validate input parameters:
   ```go
   func (t *WebFetchTool) ValidateInput(state tooltypes.State, parameters string) error {
       input := &WebFetchInput{}
       err := json.Unmarshal([]byte(parameters), input)
       if err != nil {
           return err
       }

       if input.URL == "" {
           return errors.New("url is required")
       }

       // Validate URL format and scheme
       parsedURL, err := url.Parse(input.URL)
       if err != nil {
           return fmt.Errorf("invalid URL: %w", err)
       }

       if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
           return errors.New("only HTTP and HTTPS schemes are supported")
       }

       if input.Prompt == "" {
           return errors.New("prompt is required")
       }

       return nil
   }
   ```

4. Implement the tool execution logic:
   ```go
   func (t *WebFetchTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
       input := &WebFetchInput{}
       err := json.Unmarshal([]byte(parameters), input)
       if err != nil {
           return tooltypes.ToolResult{
               Error: err.Error(),
           }
       }

       // 1. Fetch the content with a custom HTTP client that handles same-domain redirects
       content, contentType, err := fetchWithSameDomainRedirects(input.URL)
       if err != nil {
           return tooltypes.ToolResult{
               Error: fmt.Sprintf("Failed to fetch URL: %s", err),
           }
       }

       // 2. Convert HTML to Markdown if appropriate
       var processedContent string
       if strings.Contains(contentType, "text/html") {
           processedContent = convertHTMLToMarkdown(content)
       } else {
           processedContent = content
       }

       // 3. Use weak LLM to extract the requested information
       subAgentConfig, ok := ctx.Value(llmtypes.SubAgentConfig{}).(llmtypes.SubAgentConfig)
       if !ok {
           return tooltypes.ToolResult{
               Error: "sub-agent config not found in context",
           }
       }

       // Create a prompt for information extraction
       extractionPrompt := fmt.Sprintf("Here is content from %s. %s\n\n%s",
           input.URL, input.Prompt, processedContent)

       handler := subAgentConfig.MessageHandler
       if handler == nil {
           logrus.Warn("no message handler found in context, using console handler")
           handler = &llmtypes.ConsoleMessageHandler{}
       }

       // Use weak model for extraction
       extractedInfo, err := subAgentConfig.Thread.SendMessage(ctx, extractionPrompt, handler, llmtypes.MessageOpt{
           UseWeakModel: true,
           PromptCache:  false,
       })

       if err != nil {
           return tooltypes.ToolResult{
               Error: fmt.Sprintf("Failed to extract information: %s", err),
           }
       }

       return tooltypes.ToolResult{
           Result: extractedInfo,
       }
   }
   ```

5. Helper functions for HTTP fetching and HTML-to-Markdown conversion:
   ```go
   // fetchWithSameDomainRedirects fetches content from a URL and follows redirects
   // only if they stay within the same domain
   func fetchWithSameDomainRedirects(urlStr string) (string, string, error) {
       // Implementation details for HTTP client with custom redirect policy
       // ...
   }

   // convertHTMLToMarkdown converts HTML content to Markdown
   func convertHTMLToMarkdown(htmlContent string) string {
       // Implementation details for HTML to Markdown conversion
       // ...
   }
   ```

6. Add tracing for observability:
   ```go
   func (t *WebFetchTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
       input := &WebFetchInput{}
       err := json.Unmarshal([]byte(parameters), input)
       if err != nil {
           return nil, err
       }

       return []attribute.KeyValue{
           attribute.String("url", input.URL),
       }, nil
   }
   ```

7. Register the tool with the existing tool registration mechanism in the main application:
   ```go
   // Add to the appropriate initialization code where tools are registered
   tools := []tooltypes.Tool{
       // Existing tools...
       &tools.WebFetchTool{},
   }
   ```

## Consequences

### Positive
- Enables Kodelet to access web content directly, expanding its capabilities
- Improves user experience by eliminating manual copy-paste of web content
- HTML-to-Markdown conversion makes web content more readable in the CLI context
- Information extraction with LLM reduces noise and focuses on relevant content
- Same-domain redirect policy provides a security boundary

### Negative
- Increases dependency footprint (HTTP client, HTML-to-Markdown library)
- Potential for misuse to access inappropriate content
- May increase token usage due to processing of web content
- Network failures could impact tool reliability
- HTML parsing and conversion may not be perfect for all websites

### Neutral
- Requires additional testing for various web content formats
- May need rate limiting for frequently accessed URLs
- Content extraction quality depends on the LLM's capabilities

## Implementation Plan

1. Add dependencies:
   - HTTP client library (standard library `net/http`)
   - HTML-to-Markdown conversion library (e.g., `github.com/JohannesKaufmann/html-to-markdown`)

2. Create the new tool implementation:
   ```
   pkg/
    └── tools/
        └── web_fetch.go       # Main tool implementation with all HTTP utilities included
   ```

3. Implement helper functions:
   - Custom HTTP client with same-domain redirect policy
   - HTML-to-Markdown conversion
   - Content type detection and handling

4. Update the tool registry to include the new tool

5. Add tests:
   - Unit tests for validation logic
   - Integration tests with mock HTTP responses
   - Tests for same-domain redirect policy
   - Tests for HTML-to-Markdown conversion
   - Tests for error handling

6. Update documentation to include the new tool

## Alternatives Considered

1. **Browser automation**:
   - Use headless browser (e.g., Playwright, Puppeteer) to fetch web content
   - Advantages: Better rendering of JavaScript-heavy sites
   - Rejected due to significantly heavier dependencies and complexity

2. **Proxy through external service**:
   - Send URLs to an external service that handles fetching and processing
   - Advantages: Reduced client-side dependencies, potentially better rendering
   - Rejected due to privacy concerns and external dependencies

3. **URL-to-text conversion without extraction**:
   - Simply convert web content to text without using LLM for extraction
   - Advantages: Simpler implementation, less token usage
   - Rejected because extracted information is more valuable than raw content

4. **No content conversion**:
   - Return raw HTML content without converting to Markdown
   - Advantages: Simpler implementation
   - Rejected because Markdown is more readable in CLI context
