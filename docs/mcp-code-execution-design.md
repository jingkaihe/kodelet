# MCP Code Execution Design

## Overview

This document outlines the design for implementing code execution with MCP in Kodelet, based on Anthropic's blog post: https://www.anthropic.com/engineering/code-execution-with-mcp

## Problem Statement

Current MCP implementation has two key inefficiencies:

1. **Tool definition overhead**: All MCP tool definitions are loaded upfront and sent to the LLM in every request, consuming excessive context tokens
2. **Intermediate result overhead**: Data flows through the model multiple times (e.g., fetching a document, then writing it somewhere requires the full document content passing through the model twice)

## Solution: Code Execution with MCP

Instead of exposing MCP tools as direct function calls, present them as a code API that the agent can import and call from generated code. This allows:

- **Progressive disclosure**: Load only tool definitions the agent needs
- **Context efficiency**: Process data in the execution environment, only return final results
- **Better control flow**: Use native code constructs (loops, conditionals, error handling)
- **Privacy preservation**: Intermediate data stays in execution environment
- **State persistence**: Save results to files for later use
- **Type safety**: Leverage MCP output schemas (2025-06-18) for predictable, type-safe tool chaining

## Architecture

### Components

```
┌─────────────────────────────────────────────────────────────┐
│                        Kodelet Agent                         │
├─────────────────────────────────────────────────────────────┤
│  LLM Thread                                                  │
│  ├─ Standard tools (bash, file_read, etc.)                 │
│  └─ code_execution tool (NEW)                              │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│              Code Execution Environment (NEW)                │
├─────────────────────────────────────────────────────────────┤
│  Runtime: Node.js with tsx (TypeScript execution)              │
│  ├─ Filesystem: Workspace directory access                     │
│  └─ Network: Access for MCP RPC communication                  │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│                MCP Tool Filesystem (NEW)                     │
├─────────────────────────────────────────────────────────────┤
│  .kodelet/mcp/                                              │
│  ├─ servers/                                                │
│  │   ├─ google-drive/                                       │
│  │   │   ├─ getDocument.ts                                 │
│  │   │   ├─ listFiles.ts                                   │
│  │   │   └─ index.ts                                       │
│  │   ├─ salesforce/                                        │
│  │   │   ├─ updateRecord.ts                                │
│  │   │   ├─ query.ts                                       │
│  │   │   └─ index.ts                                       │
│  │   └─ ...                                                 │
│  └─ client.ts (MCP client wrapper)                         │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│                    MCP Manager                               │
│                (Existing - Modified)                         │
├─────────────────────────────────────────────────────────────┤
│  ├─ MCPClient (stdio/SSE)                                   │
│  └─ Tool Registry                                           │
└─────────────────────────────────────────────────────────────┘
```

### File Structure

```
pkg/
├─ mcp/                         (NEW: MCP-specific functionality)
│   ├─ codegen/                 (Code generation for MCP tools)
│   │   ├─ generator.go         (Code generator implementation)
│   │   ├─ generator_test.go
│   │   └─ templates/           (TypeScript templates)
│   │       ├─ client.ts.tmpl
│   │       ├─ tool.ts.tmpl
│   │       └─ example.ts.tmpl
│   ├─ rpc/                     (RPC bridge for code execution)
│   │   ├─ server.go            (MCP RPC server)
│   │   └─ server_test.go
│   └─ runtime/                 (Code execution runtime)
│       ├─ node.go              (Node.js/tsx runtime wrapper)
│       └─ node_test.go
│
├─ tools/
│   ├─ code_execution.go        (NEW: Code execution tool)
│   ├─ code_execution_test.go   (NEW)
│   └─ mcp.go                   (EXISTING: MCP tool wrapper)
│
└─ types/
    └─ tools/
        └─ code_execution.go    (NEW: Types for code execution)
```

### Implementation Phases

The implementation is structured in 6 phases, each building on the previous and providing standalone value:

**Phase 1: MCP Tool Filesystem Generation** - Generate TypeScript API files for MCP tools. Provides standalone value for manual scripting.

**Phase 2: CLI Support for MCP Tools** - Add `kodelet mcp` commands to list, generate, and call MCP tools from CLI. Makes MCP tools immediately useful.

**Phase 3: MCP RPC Bridge** - Create RPC server for code execution environment to call MCP tools. Bridge between generated code and MCP manager.

**Phase 4: Code Execution Environment** - Add Node.js/tsx runtime and `code_execution` tool for automated agent code execution.

**Phase 5: Integration and Configuration** - Wire everything together with config options and startup logic.

**Phase 6: Testing and Optimization** - Comprehensive testing and performance monitoring.

Each phase can be tested and deployed independently, allowing for iterative development and feedback.

## Phase 1: MCP Tool Filesystem Generation (Foundation)

**Goal**: Generate TypeScript API files for MCP tools that can be used standalone or by code execution

This phase provides value on its own - developers can manually write TypeScript to interact with MCP tools before we add automated code execution.

### 1.1 Code Generator Implementation

Create `pkg/mcp/codegen/generator.go`:

```go
package codegen

import (
    "context"
    "os"
    "path/filepath"
    "text/template"
    
    "github.com/jingkaihe/kodelet/pkg/tools"
)

type MCPCodeGenerator struct {
    mcpManager  *tools.MCPManager
    outputDir   string
    templates   *template.Template
}

func NewMCPCodeGenerator(manager *tools.MCPManager, outputDir string) *MCPCodeGenerator {
    // Load templates from pkg/mcp/codegen/templates/
    return &MCPCodeGenerator{
        mcpManager: manager,
        outputDir:  outputDir,
    }
}

func (g *MCPCodeGenerator) Generate(ctx context.Context) error {
    // 1. Create directory structure
    serversDir := filepath.Join(g.outputDir, "servers")
    os.MkdirAll(serversDir, 0755)
    
    // 2. Generate client wrapper
    if err := g.generateClient(); err != nil {
        return err
    }
    
    // 3. Generate example script
    if err := g.generateExample(); err != nil {
        return err
    }
    
    // 4. For each MCP server, generate tool files
    tools, err := g.mcpManager.ListMCPTools(ctx)
    if err != nil {
        return err
    }
    
    // Group by server
    toolsByServer := make(map[string][]MCPTool)
    for _, tool := range tools {
        serverName := extractServerName(tool.mcpToolName)
        toolsByServer[serverName] = append(toolsByServer[serverName], tool)
    }
    
    // Generate files for each server
    for serverName, tools := range toolsByServer {
        serverDir := filepath.Join(serversDir, serverName)
        os.MkdirAll(serverDir, 0755)
        
        for _, tool := range tools {
            if err := g.generateToolFile(serverDir, tool); err != nil {
                return err
            }
        }
        
        if err := g.generateServerIndex(serverDir, tools); err != nil {
            return err
        }
    }
    
    return nil
}

func (g *MCPCodeGenerator) generateToolFile(serverDir string, tool MCPTool) error {
    // Generate individual tool file like:
    // servers/google-drive/getDocument.ts
    
    data := struct {
        ToolName    string
        MCPToolName string
        Description string
        InputSchema interface{}
    }{
        ToolName:    sanitizeName(tool.mcpToolName),
        MCPToolName: tool.mcpToolName,
        Description: tool.Description(),
        InputSchema: tool.GenerateSchema(),
    }
    
    filename := filepath.Join(serverDir, data.ToolName+".ts")
    return g.templates.ExecuteTemplate(filename, "tool.ts.tmpl", data)
}
```

### 1.2 Template Files

Create `pkg/mcp/codegen/templates/tool.ts.tmpl`:

```typescript
// {{.ToolName}}.ts - Generated MCP tool wrapper
import { callMCPTool } from "../../client.ts";

{{if .Description}}
/**
 * {{.Description}}
 */
{{end}}
export async function {{.ToolName}}(input: {{.ToolName}}Input): Promise<{{.ToolName}}Response> {
  return callMCPTool<{{.ToolName}}Response>('{{.MCPToolName}}', input);
}

// Input type (generated from input schema)
export interface {{.ToolName}}Input {
  {{range .InputSchema.Properties}}
  {{if .Description}}
  /**
   * {{.Description}}
   {{- if .Format}}
   * @format {{.Format}}
   {{- end}}
   {{- if .Default}}
   * @default {{.Default}}
   {{- end}}
   {{- if .Enum}}
   * @enum {{.Enum}}
   {{- end}}
   {{- if .Minimum}}
   * @minimum {{.Minimum}}
   {{- end}}
   {{- if .Maximum}}
   * @maximum {{.Maximum}}
   {{- end}}
   {{- if .MinLength}}
   * @minLength {{.MinLength}}
   {{- end}}
   {{- if .MaxLength}}
   * @maxLength {{.MaxLength}}
   {{- end}}
   {{- if .Pattern}}
   * @pattern {{.Pattern}}
   {{- end}}
   */
  {{end}}
  {{.Name}}{{if not .Required}}?{{end}}: {{.TypeScriptType}};
  {{end}}
}

{{if .OutputSchema}}
// Output type (generated from output schema - type-safe!)
export interface {{.ToolName}}Response {
  {{range .OutputSchema.Properties}}
  {{if .Description}}
  /**
   * {{.Description}}
   {{- if .Format}}
   * @format {{.Format}}
   {{- end}}
   {{- if .Default}}
   * @default {{.Default}}
   {{- end}}
   {{- if .Enum}}
   * @enum {{.Enum}}
   {{- end}}
   {{- if .Minimum}}
   * @minimum {{.Minimum}}
   {{- end}}
   {{- if .Maximum}}
   * @maximum {{.Maximum}}
   {{- end}}
   {{- if .MinLength}}
   * @minLength {{.MinLength}}
   {{- end}}
   {{- if .MaxLength}}
   * @maxLength {{.MaxLength}}
   {{- end}}
   {{- if .Pattern}}
   * @pattern {{.Pattern}}
   {{- end}}
   */
  {{end}}
  {{.Name}}{{if not .Required}}?{{end}}: {{.TypeScriptType}};
  {{end}}
}
{{else}}
// Output type (no schema provided - generic)
export interface {{.ToolName}}Response {
  [key: string]: any;
}
{{end}}
```

Create `pkg/mcp/codegen/templates/client.ts.tmpl`:

```typescript
// client.ts - MCP client wrapper for code execution environment
// This file is automatically generated - do not edit

const MCP_RPC_ENDPOINT = process.env.MCP_RPC_ENDPOINT || "./.kodelet/mcp.sock";

interface MCPRequest {
  tool: string;
  arguments: Record<string, any>;
}

interface MCPResponse {
  content: Array<{ type: string; text?: string }>;
  structuredContent?: any;  // MCP 2025-06-18: structured output
  isError?: boolean;
}

export async function callMCPTool<T>(toolName: string, args: Record<string, any>): Promise<T> {
  const request: MCPRequest = {
    tool: toolName,
    arguments: args,
  };
  
  // Call MCP tool via RPC mechanism (Unix socket)
  const response = await fetch(MCP_RPC_ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  
  if (!response.ok) {
    throw new Error(`MCP tool ${toolName} failed: ${response.statusText}`);
  }
  
  const result: MCPResponse = await response.json();
  
  if (result.isError) {
    throw new Error(`MCP tool ${toolName} error: ${JSON.stringify(result.content)}`);
  }
  
  // Prefer structured content if available (MCP 2025-06-18)
  if (result.structuredContent !== undefined) {
    return result.structuredContent as T;
  }
  
  // Fallback: extract text content and parse as JSON
  const textContent = result.content
    .filter(c => c.type === "text")
    .map(c => c.text)
    .join("");
  
  try {
    return JSON.parse(textContent) as T;
  } catch {
    // If not JSON, return as-is
    return textContent as unknown as T;
  }
}
```

### 1.3 Leveraging MCP Output Schemas

**MCP Specification (2025-06-18)** introduced output schemas for tools, enabling type-safe, predictable tool chaining.

#### Benefits

1. **LLM Code Generation**: Rich TSDoc comments help LLM understand how to use tools correctly
2. **Constraint Awareness**: LLM can see and respect min/max, length, enum, pattern constraints
3. **Type Safety**: Generated TypeScript types provide compile-time checks for LLM-generated code
4. **Predictable Chaining**: Known output structure makes multi-tool workflows more reliable  
5. **Self-Documenting**: LLM can read field descriptions, defaults, and formats without external docs
6. **Structured Content**: Direct access to structured data without JSON parsing

#### Example: Type-Safe Tool Chaining

MCP tool with output schema:
```json
{
  "name": "get_customer_data",
  "outputSchema": {
    "type": "object",
    "properties": {
      "customerId": { "type": "string" },
      "email": { "type": "string" },
      "name": { "type": "string" },
      "orderCount": { "type": "number" }
    },
    "required": ["customerId", "email", "name", "orderCount"]
  }
}
```

Generated TypeScript interface (basic example without metadata):
```typescript
export interface getCustomerDataResponse {
  customerId: string;
  email: string;
  name: string;
  orderCount: number;
}
```

#### Enhanced Example with Field Metadata

MCP tool with detailed schema including metadata:
```json
{
  "name": "search_products",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query": {
        "type": "string",
        "description": "Search query string",
        "minLength": 1,
        "maxLength": 200
      },
      "category": {
        "type": "string",
        "description": "Product category filter",
        "enum": ["electronics", "clothing", "books", "all"],
        "default": "all"
      },
      "maxResults": {
        "type": "number",
        "description": "Maximum number of results to return",
        "minimum": 1,
        "maximum": 100,
        "default": 10
      },
      "sortBy": {
        "type": "string",
        "description": "Sort order for results",
        "enum": ["relevance", "price_asc", "price_desc"],
        "default": "relevance"
      }
    },
    "required": ["query"]
  },
  "outputSchema": {
    "type": "object",
    "properties": {
      "products": {
        "type": "array",
        "description": "List of matching products"
      },
      "totalCount": {
        "type": "number",
        "description": "Total number of matches (may exceed returned results)",
        "minimum": 0
      },
      "hasMore": {
        "type": "boolean",
        "description": "Whether there are more results available"
      }
    },
    "required": ["products", "totalCount", "hasMore"]
  }
}
```

Generated TypeScript with metadata comments:
```typescript
// searchProducts.ts - Generated MCP tool wrapper
import { callMCPTool } from "../../client.ts";

/**
 * Search for products in the catalog
 */
export async function searchProducts(input: searchProductsInput): Promise<searchProductsResponse> {
  return callMCPTool<searchProductsResponse>('search_products', input);
}

// Input type (generated from input schema)
export interface searchProductsInput {
  /**
   * Search query string
   * @minLength 1
   * @maxLength 200
   */
  query: string;
  
  /**
   * Product category filter
   * @enum ["electronics", "clothing", "books", "all"]
   * @default "all"
   */
  category?: string;
  
  /**
   * Maximum number of results to return
   * @minimum 1
   * @maximum 100
   * @default 10
   */
  maxResults?: number;
  
  /**
   * Sort order for results
   * @enum ["relevance", "price_asc", "price_desc"]
   * @default "relevance"
   */
  sortBy?: string;
}

// Output type (generated from output schema - type-safe!)
export interface searchProductsResponse {
  /**
   * List of matching products
   */
  products: any[];
  
  /**
   * Total number of matches (may exceed returned results)
   * @minimum 0
   */
  totalCount: number;
  
  /**
   * Whether there are more results available
   */
  hasMore: boolean;
}
```

Now when the LLM generates code, it can read the constraints and use them correctly:
```typescript
// LLM sees: query: string (required)
//   Search query string
//   @minLength 1
//   @maxLength 200
// LLM knows: must provide query, length must be 1-200 chars

const results = await searchProducts({
  query: "laptop",
  category: "electronics",  // LLM sees enum, knows valid values
  maxResults: 50            // LLM sees @minimum 1 @maximum 100, validates range
});

// LLM sees totalCount is a number in comments
// LLM sees hasMore is a boolean
// LLM understands the response structure from TSDoc
```

Type-safe chaining in code execution:
```typescript
import * as crm from './servers/crm/index.ts';
import * as email from './servers/email/index.ts';

// Get customer data - output type is known and enforced
const customer = await crm.getCustomerData({ customerId: '12345' });

// LLM can read TSDoc comments to know customer.email, customer.name, customer.orderCount
// TypeScript ensures type safety at runtime
if (customer.orderCount > 10) {
  await email.sendEmail({
    to: customer.email,  // Type-safe: we know this is a string
    subject: `Thanks ${customer.name}!`,
    body: `You've made ${customer.orderCount} orders with us.`
  });
}

console.log(`Sent email to ${customer.name} (${customer.email})`);
```

#### Code Generator Updates

The generator should:

1. **Extract output schemas** from MCP tool definitions
2. **Generate TypeScript types** from output schemas using JSON Schema to TypeScript conversion
3. **Extract field metadata** from JSON Schema properties (description, constraints, defaults)
4. **Generate TSDoc comments** for each field with all available metadata
5. **Provide fallback** for tools without output schemas (generic `any` type)

#### JSON Schema Metadata Extraction

**Why metadata matters for LLM code generation:**

When the LLM generates TypeScript code to solve a task, it reads the generated `.ts` files to understand available tools. Rich TSDoc comments enable the LLM to:

1. **Understand constraints** - See min/max values, length limits, patterns without trial and error
2. **Choose correct values** - Know valid enum options, default values, expected formats
3. **Validate inputs** - Generate code that respects constraints before execution
4. **Chain tools effectively** - Understand output structure to pass data between tools
5. **Reduce errors** - Avoid parameter mistakes by reading inline documentation

The generator needs to extract these JSON Schema attributes for each property:

| JSON Schema Field | TSDoc Annotation | Example |
|------------------|-----------------|---------|
| `description` | Main comment text | `// Search query string` |
| `default` | `@default` | `@default "all"` |
| `enum` | `@enum` | `@enum ["electronics", "clothing"]` |
| `minimum` | `@minimum` | `@minimum 1` |
| `maximum` | `@maximum` | `@maximum 100` |
| `minLength` | `@minLength` | `@minLength 1` |
| `maxLength` | `@maxLength` | `@maxLength 200` |
| `pattern` | `@pattern` | `@pattern ^[a-z]+$` |
| `format` | `@format` | `@format email` |
| `required` | Field optionality | `field?: type` vs `field: type` |

**Implementation approach:**

All JSON Schema metadata fields should be extracted and made available to the template so they can be rendered as TSDoc comments. This gives the LLM maximum information when generating code.

```go
// Helper struct for template data
type SchemaProperty struct {
    Name           string
    TypeScriptType string
    Required       bool
    Description    string
    Default        interface{}
    Enum           []interface{}
    Minimum        *float64
    Maximum        *float64
    MinLength      *int
    MaxLength      *int
    Pattern        string
    Format         string
}

func extractSchemaProperties(schema map[string]interface{}) []SchemaProperty {
    properties := []SchemaProperty{}
    
    propsMap := schema["properties"].(map[string]interface{})
    requiredFields := getRequiredFields(schema)
    
    for name, propData := range propsMap {
        prop := propData.(map[string]interface{})
        
        schemaProps := SchemaProperty{
            Name:           name,
            TypeScriptType: jsonTypeToTS(prop["type"]),
            Required:       contains(requiredFields, name),
            Description:    getString(prop, "description"),
            Default:        prop["default"],
            Enum:           getArray(prop, "enum"),
            Minimum:        getFloat(prop, "minimum"),
            Maximum:        getFloat(prop, "maximum"),
            MinLength:      getInt(prop, "minLength"),
            MaxLength:      getInt(prop, "maxLength"),
            Pattern:        getString(prop, "pattern"),
            Format:         getString(prop, "format"),
        }
        
        properties = append(properties, schemaProps)
    }
    
    return properties
}
```

**Note**: The existing `pkg/tools/mcp.go` MCPTool struct should be extended to include:
```go
type MCPTool struct {
    client              *client.Client
    mcpToolInputSchema  mcp.ToolInputSchema
    mcpToolOutputSchema mcp.ToolOutputSchema  // NEW: store output schema
    mcpToolName         string
    mcpToolDescription  string
}
```

Example generator logic:
```go
func (g *MCPCodeGenerator) generateToolFile(serverDir string, tool MCPTool) error {
    data := struct {
        ToolName     string
        MCPToolName  string
        Description  string
        InputSchema  interface{}
        OutputSchema interface{} // NEW: output schema
    }{
        ToolName:     sanitizeName(tool.mcpToolName),
        MCPToolName:  tool.mcpToolName,
        Description:  tool.Description(),
        InputSchema:  tool.InputSchema,
        OutputSchema: tool.OutputSchema,  // Extract from MCP tool
    }
    
    filename := filepath.Join(serverDir, data.ToolName+".ts")
    return g.templates.ExecuteTemplate(filename, "tool.ts.tmpl", data)
}
```

#### RPC Server Updates

The MCP RPC server should pass through structured content:

```go
func (s *MCPRPCServer) handleMCPCall(w http.ResponseWriter, r *http.Request) {
    // ... execute tool ...
    
    result := targetTool.Execute(r.Context(), nil, string(params))
    structuredData := result.StructuredData()
    
    // Return both content and structuredContent
    response := map[string]interface{}{
        "content": []map[string]interface{}{
            {"type": "text", "text": result.GetResult()},
        },
    }
    
    // Include structured content if available
    if metadata, ok := structuredData.Metadata.(*tooltypes.MCPToolMetadata); ok {
        if len(metadata.Content) > 0 {
            // Extract structured content from MCP response
            response["structuredContent"] = extractStructuredContent(metadata.Content)
        }
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}
```

### 1.4 CLI Command for Code Generation

Add a CLI command to generate MCP tool files on demand:

```bash
# Generate TypeScript API files for all configured MCP tools
kodelet mcp generate

# Generate for specific server
kodelet mcp generate --server google-drive

# Regenerate (clean and rebuild)
kodelet mcp generate --clean

# Output to custom directory
kodelet mcp generate --output .kodelet/mcp
```

Create `cmd/kodelet/mcp_generate.go`:

```go
package main

import (
    "github.com/spf13/cobra"
    "github.com/jingkaihe/kodelet/pkg/mcp/codegen"
    "github.com/jingkaihe/kodelet/pkg/tools"
    "github.com/jingkaihe/kodelet/pkg/presenter"
)

var mcpGenerateCmd = &cobra.Command{
    Use:   "generate",
    Short: "Generate TypeScript API files for MCP tools",
    Long: `Generate TypeScript API files for configured MCP tools.
    
This creates a filesystem representation of your MCP tools that can be:
- Called directly using Node.js with tsx
- Used by the code execution environment
- Inspected to understand available tools`,
    RunE: func(cmd *cobra.Command, args []string) error {
        ctx := cmd.Context()
        
        // Load MCP configuration
        mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
        if err != nil {
            return errors.Wrap(err, "failed to create MCP manager")
        }
        defer mcpManager.Close(ctx)
        
        // Get flags
        outputDir, _ := cmd.Flags().GetString("output")
        serverFilter, _ := cmd.Flags().GetString("server")
        clean, _ := cmd.Flags().GetBool("clean")
        
        // Clean if requested
        if clean {
            presenter.Info("Cleaning existing generated files...")
            os.RemoveAll(outputDir)
        }
        
        // Generate
        presenter.Info("Generating TypeScript API files...")
        generator := codegen.NewMCPCodeGenerator(mcpManager, outputDir)
        if serverFilter != "" {
            generator.SetServerFilter(serverFilter)
        }
        
        if err := generator.Generate(ctx); err != nil {
            return errors.Wrap(err, "failed to generate code")
        }
        
        // Count generated files
        stats := generator.GetStats()
        presenter.Success(fmt.Sprintf("Generated %d tools from %d servers", 
            stats.ToolCount, stats.ServerCount))
        presenter.Info(fmt.Sprintf("Output directory: %s", outputDir))
        
        // Show example usage
        presenter.Section("Example Usage")
        fmt.Printf(`
You can now call MCP tools directly using Node.js:

    npx tsx %s/example.ts

Or explore the generated API:

    ls %s/servers/
    cat %s/servers/google-drive/getDocument.ts
`, outputDir, outputDir, outputDir)
        
        return nil
    },
}

func init() {
    mcpGenerateCmd.Flags().String("output", ".kodelet/mcp", "Output directory for generated files")
    mcpGenerateCmd.Flags().String("server", "", "Generate only for specific server")
    mcpGenerateCmd.Flags().Bool("clean", false, "Clean output directory before generating")
}
```

### 1.4 Generate Example Script

The generator should also create an example script showing usage:

Create `pkg/mcp/codegen/templates/example.ts.tmpl`:

```typescript
// example.ts - Example usage of generated MCP tools
// This file is automatically generated by kodelet mcp generate

{{range .Servers}}
// Example: {{.Name}} server
import * as {{.Name}} from './servers/{{.Name}}/index.ts';

async function example{{.Name | title}}() {
  console.log("=== {{.Name}} Examples ===");
  
  {{range .Tools}}
  // {{.Description}}
  // const result = await {{$.Name}}.{{.FunctionName}}({ /* params */ });
  // console.log(result);
  {{end}}
}

{{end}}

// Run all examples
async function main() {
  {{range .Servers}}
  await example{{.Name | title}}();
  {{end}}
}

if (import.meta.main) {
  main().catch(console.error);
}
```

## Phase 2: CLI Support for MCP Tools

**Goal**: Allow calling MCP tools directly from CLI, making them useful without code execution

This provides immediate value and allows testing MCP tools manually.

### 2.1 MCP Tool Call Command

Add `kodelet mcp call` command:

```bash
# Call an MCP tool directly
kodelet mcp call google-drive.getDocument --args '{"documentId": "abc123"}'

# Interactive mode - prompts for parameters
kodelet mcp call google-drive.getDocument --interactive

# Output as JSON
kodelet mcp call google-drive.listFiles --args '{}' --json

# Save output to file
kodelet mcp call google-drive.getDocument --args '{"documentId": "abc123"}' --output doc.txt
```

Create `cmd/kodelet/mcp_call.go`:

```go
package main

import (
    "encoding/json"
    "github.com/spf13/cobra"
    "github.com/jingkaihe/kodelet/pkg/tools"
    "github.com/jingkaihe/kodelet/pkg/presenter"
)

var mcpCallCmd = &cobra.Command{
    Use:   "call TOOL_NAME",
    Short: "Call an MCP tool directly from CLI",
    Long: `Call an MCP tool with specified arguments.
    
Tool name format: server-name.tool-name
Example: google-drive.getDocument`,
    Args: cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        ctx := cmd.Context()
        toolName := args[0]
        
        // Parse tool name (server.tool)
        parts := strings.Split(toolName, ".")
        if len(parts) != 2 {
            return errors.New("tool name must be in format: server-name.tool-name")
        }
        
        // Load MCP manager
        mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
        if err != nil {
            return errors.Wrap(err, "failed to create MCP manager")
        }
        defer mcpManager.Close(ctx)
        
        // Get flags
        argsJSON, _ := cmd.Flags().GetString("args")
        interactive, _ := cmd.Flags().GetBool("interactive")
        jsonOutput, _ := cmd.Flags().GetBool("json")
        outputFile, _ := cmd.Flags().GetString("output")
        
        // Get arguments
        var argsMap map[string]interface{}
        if interactive {
            // Prompt for parameters
            argsMap, err = promptForParameters(ctx, mcpManager, toolName)
            if err != nil {
                return err
            }
        } else {
            if err := json.Unmarshal([]byte(argsJSON), &argsMap); err != nil {
                return errors.Wrap(err, "invalid JSON arguments")
            }
        }
        
        // Find and execute tool
        presenter.Info(fmt.Sprintf("Calling %s...", toolName))
        
        tools, err := mcpManager.ListMCPTools(ctx)
        if err != nil {
            return err
        }
        
        var targetTool *tools.MCPTool
        for _, tool := range tools {
            if tool.Name() == "mcp_"+strings.ReplaceAll(toolName, ".", "_") {
                targetTool = &tool
                break
            }
        }
        
        if targetTool == nil {
            return errors.New("tool not found")
        }
        
        // Execute
        params, _ := json.Marshal(argsMap)
        result := targetTool.Execute(ctx, nil, string(params))
        
        if result.IsError() {
            presenter.Error(errors.New(result.GetError()), "Tool execution failed")
            return errors.New(result.GetError())
        }
        
        // Output result
        if outputFile != "" {
            if err := os.WriteFile(outputFile, []byte(result.GetResult()), 0644); err != nil {
                return err
            }
            presenter.Success(fmt.Sprintf("Output written to %s", outputFile))
        } else if jsonOutput {
            fmt.Println(result.GetResult())
        } else {
            presenter.Section("Result")
            fmt.Println(result.GetResult())
        }
        
        return nil
    },
}

func init() {
    mcpCallCmd.Flags().String("args", "{}", "JSON arguments for the tool")
    mcpCallCmd.Flags().Bool("interactive", false, "Prompt for parameters interactively")
    mcpCallCmd.Flags().Bool("json", false, "Output result as JSON only")
    mcpCallCmd.Flags().String("output", "", "Write output to file")
}
```

### 2.2 MCP Tool Listing

Add `kodelet mcp list` command:

```bash
# List all available MCP tools
kodelet mcp list

# List tools from specific server
kodelet mcp list --server google-drive

# Show detailed information
kodelet mcp list --detailed

# Output as JSON
kodelet mcp list --json
```

Create `cmd/kodelet/mcp_list.go`:

```go
package main

import (
    "encoding/json"
    "github.com/spf13/cobra"
    "github.com/jingkaihe/kodelet/pkg/tools"
    "github.com/jingkaihe/kodelet/pkg/presenter"
)

var mcpListCmd = &cobra.Command{
    Use:   "list",
    Short: "List available MCP tools",
    RunE: func(cmd *cobra.Command, args []string) error {
        ctx := cmd.Context()
        
        // Load MCP manager
        mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
        if err != nil {
            return errors.Wrap(err, "failed to create MCP manager")
        }
        defer mcpManager.Close(ctx)
        
        // Get flags
        serverFilter, _ := cmd.Flags().GetString("server")
        detailed, _ := cmd.Flags().GetBool("detailed")
        jsonOutput, _ := cmd.Flags().GetBool("json")
        
        // List tools
        tools, err := mcpManager.ListMCPTools(ctx)
        if err != nil {
            return err
        }
        
        // Filter by server if specified
        if serverFilter != "" {
            filtered := []tools.MCPTool{}
            for _, tool := range tools {
                if strings.HasPrefix(tool.Name(), "mcp_"+serverFilter+"_") {
                    filtered = append(filtered, tool)
                }
            }
            tools = filtered
        }
        
        if jsonOutput {
            // JSON output
            data := make([]map[string]interface{}, len(tools))
            for i, tool := range tools {
                data[i] = map[string]interface{}{
                    "name":        tool.Name(),
                    "description": tool.Description(),
                }
                if detailed {
                    data[i]["schema"] = tool.GenerateSchema()
                }
            }
            output, _ := json.MarshalIndent(data, "", "  ")
            fmt.Println(string(output))
        } else {
            // Human-readable output
            presenter.Section(fmt.Sprintf("Available MCP Tools (%d)", len(tools)))
            
            // Group by server
            byServer := make(map[string][]tools.MCPTool)
            for _, tool := range tools {
                serverName := extractServerName(tool.Name())
                byServer[serverName] = append(byServer[serverName], tool)
            }
            
            for serverName, serverTools := range byServer {
                fmt.Printf("\n%s (%d tools):\n", serverName, len(serverTools))
                for _, tool := range serverTools {
                    fmt.Printf("  • %s\n", tool.Name())
                    if detailed {
                        fmt.Printf("    %s\n", tool.Description())
                    }
                }
            }
        }
        
        return nil
    },
}

func init() {
    mcpListCmd.Flags().String("server", "", "Filter by server name")
    mcpListCmd.Flags().Bool("detailed", false, "Show detailed tool information")
    mcpListCmd.Flags().Bool("json", false, "Output as JSON")
}
```

### 2.3 MCP Command Root

Create `cmd/kodelet/mcp.go`:

```go
package main

import (
    "github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
    Use:   "mcp",
    Short: "Manage and interact with MCP (Model Context Protocol) tools",
    Long: `Commands for working with MCP servers and tools.
    
MCP provides a standard way to connect AI agents to external systems.
These commands help you manage MCP servers, generate code, and call tools.`,
}

func init() {
    mcpCmd.AddCommand(mcpGenerateCmd)
    mcpCmd.AddCommand(mcpListCmd)
    mcpCmd.AddCommand(mcpCallCmd)
    rootCmd.AddCommand(mcpCmd)
}
```

## Phase 3: MCP RPC Bridge

**Goal**: Allow code execution environment to call MCP tools

### 3.1 RPC Server

Create `pkg/mcp/rpc/server.go`:

```go
package rpc

import (
    "context"
    "encoding/json"
    "net"
    "net/http"
    
    "github.com/jingkaihe/kodelet/pkg/tools"
)

// MCPRPCServer provides an RPC endpoint for code execution to call MCP tools
type MCPRPCServer struct {
    mcpManager *tools.MCPManager
    listener   net.Listener
    server     *http.Server
}

type MCPRPCRequest struct {
    Tool      string                 `json:"tool"`
    Arguments map[string]interface{} `json:"arguments"`
}

func NewMCPRPCServer(mcpManager *tools.MCPManager, socketPath string) (*MCPRPCServer, error) {
    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        return nil, err
    }
    
    s := &MCPRPCServer{
        mcpManager: mcpManager,
        listener:   listener,
    }
    
    mux := http.NewServeMux()
    mux.HandleFunc("/", s.handleMCPCall)
    
    s.server = &http.Server{Handler: mux}
    
    return s, nil
}

func (s *MCPRPCServer) handleMCPCall(w http.ResponseWriter, r *http.Request) {
    var req MCPRPCRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // Find the tool
    tools, err := s.mcpManager.ListMCPTools(r.Context())
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    var targetTool *tools.MCPTool
    for _, tool := range tools {
        if tool.mcpToolName == req.Tool {
            targetTool = &tool
            break
        }
    }
    
    if targetTool == nil {
        http.Error(w, "tool not found", http.StatusNotFound)
        return
    }
    
    // Execute the tool
    params, _ := json.Marshal(req.Arguments)
    result := targetTool.Execute(r.Context(), nil, string(params))
    
    // Return result
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(result.StructuredData())
}

func (s *MCPRPCServer) Start() error {
    return s.server.Serve(s.listener)
}

func (s *MCPRPCServer) Shutdown(ctx context.Context) error {
    return s.server.Shutdown(ctx)
}
```

## Phase 4: Code Execution Environment

**Goal**: Enable automated code execution with MCP tools

Now that we have TypeScript API generation and CLI tools, we can add automated code execution for the LLM agent.

### 4.1 Choose Runtime

**Use Node.js with tsx**

Reasons:
- Node.js is ubiquitous and likely already installed
- tsx provides seamless TypeScript execution without configuration
- No additional runtime to install (unlike Deno)
- Simple and straightforward
- Can execute TypeScript directly via stdin

Installation: `npm install -g tsx` (or use npx)

### 4.2 Runtime Wrapper

Create `pkg/mcp/runtime/node.go`:

```go
package runtime

import (
    "context"
    "os/exec"
    "strings"
)

type NodeRuntime struct {
    workspaceDir string
}

func NewNodeRuntime(workspaceDir string) *NodeRuntime {
    return &NodeRuntime{
        workspaceDir: workspaceDir,
    }
}

func (n *NodeRuntime) Execute(ctx context.Context, code string) (string, error) {
    // Use tsx to execute TypeScript code from stdin
    // npx tsx will auto-install if needed
    cmd := exec.CommandContext(ctx, "npx", "tsx", "-")
    cmd.Dir = n.workspaceDir
    cmd.Stdin = strings.NewReader(code)
    
    output, err := cmd.CombinedOutput()
    return string(output), err
}

// Name returns the name of the runtime
func (n *NodeRuntime) Name() string {
    return "node-tsx"
}
```

### 4.3 Code Execution Tool

Create `pkg/tools/code_execution.go`:

```go
package tools

import (
    "context"
    "encoding/json"
    "github.com/invopop/jsonschema"
    "github.com/jingkaihe/kodelet/pkg/mcp/runtime"
    tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type CodeExecutionTool struct {
    runtime *runtime.NodeRuntime
}

type CodeExecutionInput struct {
    Code        string `json:"code" jsonschema:"required,description=TypeScript/JavaScript code to execute"`
    Description string `json:"description,omitempty" jsonschema:"description=Brief description of what this code does"`
}

// CodeExecutionResult holds the result of code execution
type CodeExecutionResult struct {
    code    string
    output  string
    err     string
    runtime string // Runtime used (e.g., "node-tsx") - useful for debugging, telemetry, and multi-runtime support
}

func (r *CodeExecutionResult) GetResult() string {
    return r.output
}

func (r *CodeExecutionResult) GetError() string {
    return r.err
}

func (r *CodeExecutionResult) IsError() bool {
    return r.err != ""
}

func (r *CodeExecutionResult) AssistantFacing() string {
    return tooltypes.StringifyToolResult(r.output, r.err)
}

func (r *CodeExecutionResult) StructuredData() tooltypes.StructuredToolResult {
    return tooltypes.StructuredToolResult{
        ToolName:  "code_execution",
        Success:   !r.IsError(),
        Timestamp: time.Now(),
        Error:     r.err,
        Metadata: map[string]interface{}{
            "code":    r.code,
            "output":  r.output,
            "runtime": r.runtime,
        },
    }
}

func NewCodeExecutionTool(runtime *runtime.NodeRuntime) *CodeExecutionTool {
    return &CodeExecutionTool{
        runtime: runtime,
    }
}

func (t *CodeExecutionTool) Name() string {
    return "code_execution"
}

func (t *CodeExecutionTool) Description() string {
    return `Execute TypeScript/JavaScript code with access to MCP tools.

## Usage Pattern

The execution environment has access to MCP tools via the generated filesystem API.

### Example 1: Simple Tool Chain

\`\`\`typescript
import * as googleDrive from './servers/google-drive/index.ts';
import * as salesforce from './servers/salesforce/index.ts';

const doc = await googleDrive.getDocument({ documentId: 'abc123' });
const summary = doc.content.substring(0, 500); // Process data locally

await salesforce.updateRecord({
  objectType: 'Lead',
  recordId: '00Q123',
  data: { Notes: summary }
});

console.log(\`Updated with \${summary.length} char summary\`);
\`\`\`

### Example 2: Type-Safe Chaining with Output Schemas

When MCP tools provide output schemas, you get type-safe chaining:

\`\`\`typescript
import * as crm from './servers/crm/index.ts';
import * as email from './servers/email/index.ts';

// Type-safe: customer.email, customer.name, customer.orderCount are known types
const customer = await crm.getCustomerData({ customerId: '12345' });

// LLM can see the output structure from TSDoc comments
// TypeScript compiler ensures correct property access
if (customer.orderCount > 10) {
  await email.sendEmail({
    to: customer.email,      // Type-checked: string
    subject: \`Thanks \${customer.name}!\`,
    body: \`You've placed \${customer.orderCount} orders.\`
  });
  console.log(\`Sent to \${customer.email}\`);
} else {
  console.log(\`Customer \${customer.name} has only \${customer.orderCount} orders\`);
}
\`\`\`

## Tool Discovery

To discover available MCP tools, use the grep_tool to search the generated code:
- Use grep_tool with pattern "export function" and path ".kodelet/mcp/servers" to list all available functions
- Use grep_tool to search for specific tool names or descriptions
- Explore the .kodelet/mcp/servers/ directory structure to see available servers

## Best Practices

1. **Use this tool when you need to:**
   - Call a single MCP tool with output filtering/processing
   - Call multiple MCP tools in sequence (especially with output schemas for type safety)
   - Process large datasets before returning results
   - Implement complex control flow (loops, conditionals)
   - Filter/transform data between tool calls

2. **Managing output size:**
   - **CRITICAL**: Always consider the expected output size before calling MCP tools
   - For tools that may return large outputs (lists, file contents, query results):
     * Apply filtering and limit the data BEFORE logging
     * Use .slice(), .substring(), .filter() to reduce output
     * Log only summaries or counts for large datasets
   - Example for large outputs:
     \`\`\`typescript
     // BAD - may return huge output
     const files = await gdrive.listFiles({ folderId: 'root' });
     console.log(files);  // Could be thousands of files!
     
     // GOOD - filter and limit first
     const files = await gdrive.listFiles({ folderId: 'root' });
     const recentFiles = files.slice(0, 10);  // Only first 10
     console.log(\`Found \${files.length} files, showing first 10:\`);
     console.log(recentFiles.map(f => f.name).join('\\n'));
     \`\`\`

3. **Always use console.log() for outputs**
   - Only console.log() output is returned to you
   - Use it to report progress and results

4. **Keep code focused**
   - Each code execution should accomplish a specific task
   - Don't try to do too much in one execution

5. **Handle errors gracefully**
   - Use try/catch blocks
   - Log errors with console.error()

## Security Notes

- Code runs in Node.js environment using tsx
- Executes in the workspace directory with full access
- Use caution with untrusted code
- Network access available for MCP RPC communication`
}

func (t *CodeExecutionTool) GenerateSchema() *jsonschema.Schema {
    reflector := jsonschema.Reflector{
        DoNotReference: true,
    }
    return reflector.Reflect(&CodeExecutionInput{})
}

func (t *CodeExecutionTool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
    return nil, nil
}

func (t *CodeExecutionTool) ValidateInput(_ tooltypes.State, _ string) error {
    return nil
}

func (t *CodeExecutionTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
    var input CodeExecutionInput
    if err := json.Unmarshal([]byte(parameters), &input); err != nil {
        return &CodeExecutionResult{
            err:     fmt.Sprintf("invalid parameters: %v", err),
            runtime: t.runtime.Name(),
        }
    }
    
    // Execute code
    output, err := t.runtime.Execute(ctx, input.Code)
    if err != nil {
        return &CodeExecutionResult{
            code:    input.Code,
            output:  output,
            err:     fmt.Sprintf("execution failed: %v", err),
            runtime: t.runtime.Name(),
        }
    }
    
    return &CodeExecutionResult{
        code:    input.Code,
        output:  output,
        runtime: t.runtime.Name(),
    }
}
```

## Phase 5: Integration and Configuration

### 5.1 Configuration

Add to `config.sample.yaml`:

```yaml
mcp:
  # Execution mode for MCP tools
  # - "direct": Traditional direct tool calling (default)
  # - "code": Code execution with filesystem API
  execution_mode: "code"
  
  # Code execution settings (only used when execution_mode = "code")
  code_execution:
    workspace_dir: ".kodelet/mcp"  # Where to generate tool files
    regenerate_on_startup: true    # Regenerate tool files on startup
  
  # MCP servers configuration (unchanged)
  servers:
    google-drive:
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-gdrive"]
      envs:
        GDRIVE_API_KEY: "$GOOGLE_API_KEY"
```

### 5.2 Initialization Flow

Update `cmd/kodelet/run.go`:

```go
import (
    "github.com/jingkaihe/kodelet/pkg/mcp/codegen"
    "github.com/jingkaihe/kodelet/pkg/mcp/rpc"
    "github.com/jingkaihe/kodelet/pkg/mcp/runtime"
    "github.com/jingkaihe/kodelet/pkg/tools"
    "github.com/jingkaihe/kodelet/pkg/presenter"
)

// Create MCP manager
mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
if err != nil {
    return err
}

// Check if code execution mode is enabled
executionMode := viper.GetString("mcp.execution_mode")
if executionMode == "code" {
    // Initialize code execution environment
    workspaceDir := viper.GetString("mcp.code_execution.workspace_dir")
    
    // Generate MCP tool filesystem (if enabled or doesn't exist)
    shouldGenerate := viper.GetBool("mcp.code_execution.regenerate_on_startup")
    if shouldGenerate || !fileExists(filepath.Join(workspaceDir, "client.ts")) {
        presenter.Info("Generating MCP tool filesystem...")
        generator := codegen.NewMCPCodeGenerator(mcpManager, workspaceDir)
        if err := generator.Generate(ctx); err != nil {
            return err
        }
    }
    
    // Start MCP RPC server
    socketPath := filepath.Join(".kodelet", "mcp.sock")
    rpcServer, err := rpc.NewMCPRPCServer(mcpManager, socketPath)
    if err != nil {
        return err
    }
    go rpcServer.Start()
    defer rpcServer.Shutdown(ctx)
    
    // Create code execution tool
    nodeRuntime := runtime.NewNodeRuntime(workspaceDir)
    codeExecTool := tools.NewCodeExecutionTool(nodeRuntime)
    
    // Add to state (MCP tools NOT added in code mode)
    appState := tools.NewBasicState(ctx,
        tools.WithTools(append(tools.GetMainTools(), codeExecTool)),
        tools.WithCustomTools(customManager))
    
    presenter.Success("MCP code execution mode enabled")
} else {
    // Traditional mode: add MCP tools directly
    appState := tools.NewBasicState(ctx,
        tools.WithTools(tools.GetMainTools()),
        tools.WithMCPTools(mcpManager),
        tools.WithCustomTools(customManager))
    
    if mcpManager != nil {
        tools, _ := mcpManager.ListMCPTools(ctx)
        presenter.Info(fmt.Sprintf("Loaded %d MCP tools (direct mode)", len(tools)))
    }
}
```

### 5.3 Dependency Check (Optional)

Optionally add a check that `npx` is available when code execution mode is enabled:

```go
func checkNodeInstalled() error {
    cmd := exec.Command("npx", "--version")
    if err := cmd.Run(); err != nil {
        return errors.New(`Node.js/npx is not installed. 

Code execution mode requires Node.js. Install it:
  
  macOS: brew install node
  Linux: Use your package manager (apt, yum, etc.)
  Windows: Download from https://nodejs.org
  
Or disable code execution mode in config:
  
  mcp:
    execution_mode: "direct"`)
    }
    return nil
}
```

## Phase 6: Testing and Optimization

### 6.1 Unit Tests

```go
// pkg/mcp/runtime/node_test.go
func TestNodeExecution(t *testing.T) {
    runtime := NewNodeRuntime("/tmp/test")
    
    code := `console.log("Hello from Node.js");`
    output, err := runtime.Execute(context.Background(), code)
    
    assert.NoError(t, err)
    assert.Contains(t, output, "Hello from Node.js")
}

// pkg/tools/code_execution_test.go
func TestCodeExecutionTool(t *testing.T) {
    // Test basic execution
    // Test MCP tool calling
    // Test error handling
}
```

## Migration Strategy

### Phase 1: Opt-in Feature (Recommended Start)

- Add `execution_mode: "direct"` as default
- Allow users to enable `execution_mode: "code"` in config
- Both modes coexist
- Gather feedback and metrics

### Phase 2: Smart Mode Selection

- Automatically choose mode based on:
  - Number of MCP tools configured
  - Historical usage patterns
  - Tool complexity
- Add `execution_mode: "auto"` option

### Phase 3: Code-first (Future)

- Make code mode default for most scenarios
- Keep direct mode for:
  - Simple single-tool calls
  - Low-latency requirements
  - Legacy compatibility

## Success Metrics

1. **Token Usage**
   - Target: 70%+ reduction in token usage for multi-tool workflows
   - Measure: Average tokens per request (before/after)

2. **Latency**
   - Target: Neutral or better latency despite code execution overhead
   - Measure: Time to first token, total request time

3. **Adoption**
   - Target: 30%+ of users enable code mode within 3 months
   - Measure: Config analysis, telemetry

4. **Reliability**
   - Target: <1% error rate from code execution
   - Measure: Tool execution success rate

## Usage Workflows

### Workflow 1: CLI Tool Calls (Phase 2)

For quick ad-hoc MCP tool invocations:

```bash
# List available tools
kodelet mcp list

# Call a tool
kodelet mcp call google-drive.getDocument --args '{"documentId": "abc123"}'

# Interactive mode (prompts for parameters)
kodelet mcp call google-drive.getDocument --interactive

# Save to file
kodelet mcp call google-drive.getDocument \
  --args '{"documentId": "abc123"}' \
  --output document.txt
```

### Workflow 2: Agent with Code Execution (Phase 1-5)

For automated agent workflows with code execution:

```bash
# 1. Enable code execution in config
cat >> ~/.kodelet/config.yaml <<EOF
mcp:
  execution_mode: "code"
  code_execution:
    workspace_dir: ".kodelet/mcp"
    regenerate_on_startup: true
EOF

# 2. Run kodelet - it will generate API and enable code_execution tool
kodelet run "Fetch my Google Drive document abc123 and update Salesforce lead 00Q123 with a summary"

# The agent will write code like:
# import * as gdrive from './servers/google-drive/index.ts';
# import * as salesforce from './servers/salesforce/index.ts';
# const doc = await gdrive.getDocument({ documentId: 'abc123' });
# const summary = doc.content.substring(0, 500);
# await salesforce.updateRecord({ ... });
```

### Workflow 3: Development and Testing (All Phases)

For developers working on MCP integration:

```bash
# Generate and inspect
kodelet mcp generate --clean
cat .kodelet/mcp/example.ts

# Test individual tools
kodelet mcp call filesystem.list --args '{"path": "."}' --json

# Test code execution manually
cat > test.ts <<EOF
import * as gdrive from './.kodelet/mcp/servers/google-drive/index.ts';
console.log('Testing MCP tool access from TypeScript');
EOF
npx tsx test.ts

# Run agent in code mode
KODELET_LOG_LEVEL=debug kodelet run "test query"
```

## Migration Path

### Step 1: Start with Direct Mode (Current)

All users start here - existing behavior, no changes needed:

```yaml
mcp:
  execution_mode: "direct"  # or omit - this is default
  servers:
    google-drive: { ... }
```

### Step 2: Add CLI Commands (Phase 1-2)

Generate TypeScript API and use CLI tools:

```bash
kodelet mcp generate
kodelet mcp list
kodelet mcp call google-drive.getDocument --args '{...}'
```

### Step 3: Enable Code Mode (Phase 3-5)

Opt into code execution for efficiency:

```yaml
mcp:
  execution_mode: "code"
```

### Step 4: Monitor and Optimize (Phase 6)

Compare metrics and tune:
- Token usage: direct vs code mode
- Latency: time to first token
- Error rates: code execution failures
- Adoption: percentage of users enabling code mode

## CLI Command Reference

### `kodelet mcp generate`

Generate TypeScript API files for MCP tools.

```bash
kodelet mcp generate                        # Generate all
kodelet mcp generate --server google-drive  # Generate one server
kodelet mcp generate --clean                # Regenerate from scratch
kodelet mcp generate --output ./mcp-api     # Custom output dir
```

### `kodelet mcp list`

List available MCP tools.

```bash
kodelet mcp list                      # List all
kodelet mcp list --server google-drive # Filter by server
kodelet mcp list --detailed           # Show descriptions
kodelet mcp list --json               # JSON output
```

### `kodelet mcp call`

Call an MCP tool directly.

```bash
kodelet mcp call SERVER.TOOL --args '{...}'  # Direct call
kodelet mcp call SERVER.TOOL --interactive   # Interactive prompts
kodelet mcp call SERVER.TOOL --json          # JSON output only
kodelet mcp call SERVER.TOOL --output file   # Save to file
```

## References

- Anthropic blog post: https://www.anthropic.com/engineering/code-execution-with-mcp
- Cloudflare "Code Mode": https://blog.cloudflare.com/code-mode/
- MCP Specification: https://modelcontextprotocol.io/
- tsx (TypeScript execution): https://github.com/privatenumber/tsx
- Node.js: https://nodejs.org
