// Package codegen provides code generation for MCP tools.
// It generates TypeScript API files from MCP tool definitions.
package codegen

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"unicode"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pkg/errors"
)

//go:embed templates/tool.ts.tmpl
var toolTemplate string

//go:embed templates/client.ts.tmpl
var clientTemplate string

// MCPCodeGenerator generates TypeScript code from MCP tool definitions
type MCPCodeGenerator struct {
	mcpManager   *tools.MCPManager
	outputDir    string
	templates    *template.Template
	serverFilter string
	stats        GeneratorStats
}

// GeneratorStats holds statistics about the code generation
type GeneratorStats struct {
	ServerCount int
	ToolCount   int
}

// SchemaProperty represents a property from a JSON schema with all metadata
type SchemaProperty struct {
	Name           string
	TypeScriptType string
	Required       bool
	Description    string
	Default        any
	Enum           []any
	Minimum        *float64
	Maximum        *float64
	MinLength      *int
	MaxLength      *int
	Pattern        string
	Format         string
	// For arrays with object items
	ArrayItemProperties []SchemaProperty
	IsArrayOfObjects    bool
}

// ToolData holds template data for generating a tool file
type ToolData struct {
	ToolName        string
	MCPToolName     string
	ServerName      string
	Description     string
	InputSchema     *SchemaData
	HasOutputSchema bool
	OutputSchema    *SchemaData
}

// SchemaData holds parsed schema information for templates
type SchemaData struct {
	Properties []SchemaProperty
}

// ToolInfo holds basic tool information for examples
type ToolInfo struct {
	FunctionName string
	Description  string
}

// NewMCPCodeGenerator creates a new code generator
func NewMCPCodeGenerator(manager *tools.MCPManager, outputDir string) *MCPCodeGenerator {
	tmpl := template.New("mcp").Funcs(template.FuncMap{
		"title": toTitle,
	})

	template.Must(tmpl.New("tool.ts.tmpl").Parse(toolTemplate))
	template.Must(tmpl.New("client.ts.tmpl").Parse(clientTemplate))

	return &MCPCodeGenerator{
		mcpManager: manager,
		outputDir:  outputDir,
		templates:  tmpl,
	}
}

// toTitle converts a string to title case (replacement for deprecated strings.Title)
func toTitle(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// SetServerFilter sets a filter to only generate code for a specific server
func (g *MCPCodeGenerator) SetServerFilter(serverName string) {
	g.serverFilter = serverName
}

// GetStats returns generation statistics
func (g *MCPCodeGenerator) GetStats() GeneratorStats {
	return g.stats
}

// Generate generates all TypeScript code for MCP tools
func (g *MCPCodeGenerator) Generate(ctx context.Context) error {
	logger.G(ctx).Info("Starting MCP code generation")

	// Create directory structure
	serversDir := filepath.Join(g.outputDir, "servers")
	if err := os.MkdirAll(serversDir, 0o755); err != nil {
		return errors.Wrap(err, "failed to create servers directory")
	}

	// Generate package.json for ES module support
	if err := g.generatePackageJSON(); err != nil {
		return errors.Wrap(err, "failed to generate package.json")
	}

	// Generate client wrapper
	if err := g.generateClient(); err != nil {
		return errors.Wrap(err, "failed to generate client")
	}

	// // Get all MCP tools
	toolInfos := []ToolInfo{}
	var listToolsErr error
	g.mcpManager.ListMCPToolsIter(ctx, func(serverName string, _ *client.Client, tools []mcp.Tool) {
		if g.serverFilter != "" && serverName != g.serverFilter {
			return
		}
		g.stats.ServerCount++
		g.stats.ToolCount += len(tools)

		serverDir := filepath.Join(serversDir, serverName)
		if err := os.MkdirAll(serverDir, 0o755); err != nil {
			listToolsErr = errors.Wrapf(err, "failed to create directory for server %s", serverName)
			return
		}

		for _, tool := range tools {
			toolInfo, err := g.generateToolFile(serverDir, serverName, tool)
			if err != nil {
				listToolsErr = errors.Wrapf(err, "failed to generate tool file for %s", tool.GetName())
				return
			}
			toolInfos = append(toolInfos, toolInfo)
		}

		if err := g.generateServerIndex(serverDir, tools); err != nil {
			listToolsErr = errors.Wrapf(err, "failed to generate index for server %s", serverName)
			return
		}
	})
	if listToolsErr != nil {
		return listToolsErr
	}

	logger.G(ctx).WithField("servers", g.stats.ServerCount).WithField("tools", g.stats.ToolCount).Info("MCP code generation completed")

	return nil
}

// generatePackageJSON generates a package.json file for ES module support
func (g *MCPCodeGenerator) generatePackageJSON() error {
	packageJSONPath := filepath.Join(g.outputDir, "package.json")
	packageJSON := map[string]any{
		"name":        "kodelet-mcp-workspace",
		"version":     "1.0.0",
		"type":        "module",
		"description": "Kodelet MCP tool workspace for code execution",
		"private":     true,
	}

	jsonData, err := json.MarshalIndent(packageJSON, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(packageJSONPath, jsonData, 0o644)
}

// generateClient generates the MCP client wrapper
func (g *MCPCodeGenerator) generateClient() error {
	clientPath := filepath.Join(g.outputDir, "client.ts")
	f, err := os.Create(clientPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return g.templates.ExecuteTemplate(f, "client.ts.tmpl", nil)
}

// generateToolFile generates a TypeScript file for a single tool
func (g *MCPCodeGenerator) generateToolFile(serverDir string, serverName string, tool mcp.Tool) (ToolInfo, error) {
	mcpToolName := tool.GetName()
	toolName := sanitizeName(mcpToolName)

	// Parse input schema
	inputSchema, err := parseSchema(tool.InputSchema)
	if err != nil {
		return ToolInfo{}, errors.Wrap(err, "failed to parse input schema")
	}

	hasOutputSchema := true
	outputSchema, err := parseSchema(tool.OutputSchema)
	if err != nil {
		hasOutputSchema = false
		outputSchema = nil
	}

	data := ToolData{
		ToolName:        toolName,
		MCPToolName:     mcpToolName,
		ServerName:      serverName,
		Description:     tool.Description,
		InputSchema:     inputSchema,
		HasOutputSchema: hasOutputSchema,
		OutputSchema:    outputSchema,
	}

	filename := filepath.Join(serverDir, toolName+".ts")
	f, err := os.Create(filename)
	if err != nil {
		return ToolInfo{}, err
	}
	defer f.Close()

	if err := g.templates.ExecuteTemplate(f, "tool.ts.tmpl", data); err != nil {
		return ToolInfo{}, err
	}

	return ToolInfo{
		FunctionName: toolName,
		Description:  tool.Description,
	}, nil
}

// generateServerIndex generates an index.ts file that exports all tools from a server
func (g *MCPCodeGenerator) generateServerIndex(serverDir string, serverTools []mcp.Tool) error {
	indexPath := filepath.Join(serverDir, "index.ts")
	f, err := os.Create(indexPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write header comment
	fmt.Fprintf(f, "// index.ts - Auto-generated exports for MCP tools\n")
	fmt.Fprintf(f, "// This file is automatically generated - do not edit\n\n")

	// Export all tool functions
	for _, tool := range serverTools {
		toolName := sanitizeName(tool.GetName())
		fmt.Fprintf(f, "export { %s } from './%s.js';\n", toolName, toolName)
	}

	return nil
}

// parseSchema parses a JSON schema into SchemaData
func parseSchema(sch any) (*SchemaData, error) {
	schemaJSON, err := json.Marshal(sch)
	if err != nil {
		return nil, err
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return nil, err
	}

	properties := extractSchemaProperties(schema)
	return &SchemaData{Properties: properties}, nil
}

// extractSchemaProperties extracts property information from a JSON schema
func extractSchemaProperties(schema map[string]any) []SchemaProperty {
	properties := []SchemaProperty{}

	propsMap, ok := schema["properties"].(map[string]any)
	if !ok {
		return properties
	}

	requiredFields := getRequiredFields(schema)

	for name, propData := range propsMap {
		prop, ok := propData.(map[string]any)
		if !ok {
			continue
		}

		schemaProp := SchemaProperty{
			Name:        name,
			Required:    contains(requiredFields, name),
			Description: getString(prop, "description"),
			Default:     prop["default"],
			Enum:        getArray(prop, "enum"),
			Minimum:     getFloat(prop, "minimum"),
			Maximum:     getFloat(prop, "maximum"),
			MinLength:   getInt(prop, "minLength"),
			MaxLength:   getInt(prop, "maxLength"),
			Pattern:     getString(prop, "pattern"),
			Format:      getString(prop, "format"),
		}

		// Build TypeScript type recursively
		schemaProp.TypeScriptType = buildTypeScriptType(prop, requiredFields)

		// Handle array types with object items (for template inline rendering)
		typeStr, _ := prop["type"].(string)
		if typeStr == "array" {
			if items, ok := prop["items"].(map[string]any); ok {
				itemType, _ := items["type"].(string)
				if itemType == "object" {
					// Extract nested properties from array items
					schemaProp.IsArrayOfObjects = true
					schemaProp.ArrayItemProperties = extractSchemaProperties(items)
				}
			}
		}

		properties = append(properties, schemaProp)
	}

	return properties
}

// buildTypeScriptType recursively builds a TypeScript type string from a JSON schema property
func buildTypeScriptType(prop map[string]any, requiredFields []string) string {
	typeVal, ok := prop["type"]
	if !ok {
		return "any"
	}

	typeStr, ok := typeVal.(string)
	if !ok {
		return "any"
	}

	switch typeStr {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		if items, ok := prop["items"].(map[string]any); ok {
			itemType, _ := items["type"].(string)
			if itemType == "object" {
				// Recursively build inline object type for array items
				inlineType := buildInlineObjectType(items, requiredFields)
				return fmt.Sprintf("Array<%s>", inlineType)
			}
			// For non-object items, recursively get the item type
			itemTypeStr := buildTypeScriptType(items, requiredFields)
			return fmt.Sprintf("Array<%s>", itemTypeStr)
		}
		return "Array<any>"
	case "object":
		// Build inline object type
		return buildInlineObjectType(prop, requiredFields)
	default:
		return "any"
	}
}

// buildInlineObjectType builds an inline TypeScript object type from a JSON schema
func buildInlineObjectType(schema map[string]any, parentRequiredFields []string) string {
	propsMap, ok := schema["properties"].(map[string]any)
	if !ok {
		return "Record<string, any>"
	}

	requiredFields := getRequiredFields(schema)
	if len(requiredFields) == 0 {
		requiredFields = parentRequiredFields
	}

	var parts []string
	for name, propData := range propsMap {
		prop, ok := propData.(map[string]any)
		if !ok {
			continue
		}

		// Build nested type recursively
		tsType := buildTypeScriptType(prop, requiredFields)

		// Add optional marker if not required
		optional := ""
		if !contains(requiredFields, name) {
			optional = "?"
		}

		parts = append(parts, fmt.Sprintf("%s%s: %s", name, optional, tsType))
	}

	if len(parts) == 0 {
		return "Record<string, any>"
	}

	// Format as inline object type
	return fmt.Sprintf("{ %s }", strings.Join(parts, "; "))
}

// Helper functions

func sanitizeName(name string) string {
	// Convert snake_case to camelCase
	parts := strings.Split(name, "_")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	result := strings.Join(parts, "")

	// Ensure first character is lowercase
	if len(result) > 0 {
		result = strings.ToLower(result[:1]) + result[1:]
	}

	return result
}

func getRequiredFields(schema map[string]any) []string {
	required := []string{}
	if reqVal, ok := schema["required"]; ok {
		if reqArr, ok := reqVal.([]any); ok {
			for _, r := range reqArr {
				if rStr, ok := r.(string); ok {
					required = append(required, rStr)
				}
			}
		}
	}
	return required
}

func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

func getString(m map[string]any, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getArray(m map[string]any, key string) []any {
	if val, ok := m[key]; ok {
		if arr, ok := val.([]any); ok {
			return arr
		}
	}
	return nil
}

func getFloat(m map[string]any, key string) *float64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return &v
		case int:
			f := float64(v)
			return &f
		}
	}
	return nil
}

func getInt(m map[string]any, key string) *int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int:
			return &v
		case float64:
			i := int(v)
			return &i
		}
	}
	return nil
}
