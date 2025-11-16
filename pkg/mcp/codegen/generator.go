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
	"strings"
	"text/template"
	"unicode"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
)

//go:embed templates/tool.ts.tmpl
var toolTemplate string

//go:embed templates/client.ts.tmpl
var clientTemplate string

//go:embed templates/example.ts.tmpl
var exampleTemplate string

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
	Default        interface{}
	Enum           []interface{}
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
	Description     string
	InputSchema     *SchemaData
	HasOutputSchema bool
	OutputSchema    *SchemaData
}

// SchemaData holds parsed schema information for templates
type SchemaData struct {
	Properties []SchemaProperty
}

// ServerData holds information about a server for example generation
type ServerData struct {
	Name  string
	Tools []ToolInfo
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
	template.Must(tmpl.New("example.ts.tmpl").Parse(exampleTemplate))

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

	// Get all MCP tools
	mcpTools, err := g.mcpManager.ListMCPTools(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list MCP tools")
	}

	// Group tools by server
	toolsByServer := make(map[string][]tools.MCPTool)
	for _, tool := range mcpTools {
		serverName := tool.ServerName()

		// Apply server filter if set
		if g.serverFilter != "" && serverName != g.serverFilter {
			continue
		}

		toolsByServer[serverName] = append(toolsByServer[serverName], tool)
	}

	// Generate files for each server
	serverDataList := []ServerData{}
	for serverName, serverTools := range toolsByServer {
		serverDir := filepath.Join(serversDir, serverName)
		if err := os.MkdirAll(serverDir, 0o755); err != nil {
			return errors.Wrapf(err, "failed to create directory for server %s", serverName)
		}

		toolInfos := []ToolInfo{}
		for _, tool := range serverTools {
			toolInfo, err := g.generateToolFile(serverDir, tool)
			if err != nil {
				return errors.Wrapf(err, "failed to generate tool file for %s", tool.Name())
			}
			toolInfos = append(toolInfos, toolInfo)
			g.stats.ToolCount++
		}

		if err := g.generateServerIndex(serverDir, serverTools); err != nil {
			return errors.Wrapf(err, "failed to generate index for server %s", serverName)
		}

		serverDataList = append(serverDataList, ServerData{
			Name:  serverName,
			Tools: toolInfos,
		})
		g.stats.ServerCount++
	}

	// Generate example script
	if err := g.generateExample(serverDataList); err != nil {
		return errors.Wrap(err, "failed to generate example")
	}

	logger.G(ctx).WithField("servers", g.stats.ServerCount).WithField("tools", g.stats.ToolCount).Info("MCP code generation completed")

	return nil
}

// generatePackageJSON generates a package.json file for ES module support
func (g *MCPCodeGenerator) generatePackageJSON() error {
	packageJSONPath := filepath.Join(g.outputDir, "package.json")
	packageJSON := map[string]interface{}{
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
func (g *MCPCodeGenerator) generateToolFile(serverDir string, tool tools.MCPTool) (ToolInfo, error) {
	mcpToolName := tool.MCPToolName()
	toolName := sanitizeName(mcpToolName)

	// Parse input schema
	inputSchema, err := parseSchema(tool.GenerateSchema())
	if err != nil {
		return ToolInfo{}, errors.Wrap(err, "failed to parse input schema")
	}

	data := ToolData{
		ToolName:        toolName,
		MCPToolName:     mcpToolName,
		Description:     tool.Description(),
		InputSchema:     inputSchema,
		HasOutputSchema: false,
		OutputSchema:    nil,
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
		Description:  tool.Description(),
	}, nil
}

// generateServerIndex generates an index.ts file that exports all tools from a server
func (g *MCPCodeGenerator) generateServerIndex(serverDir string, serverTools []tools.MCPTool) error {
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
		toolName := sanitizeName(tool.MCPToolName())
		fmt.Fprintf(f, "export { %s } from './%s.js';\n", toolName, toolName)
	}

	return nil
}

// generateExample generates an example TypeScript file showing usage
func (g *MCPCodeGenerator) generateExample(servers []ServerData) error {
	examplePath := filepath.Join(g.outputDir, "example.ts")
	f, err := os.Create(examplePath)
	if err != nil {
		return err
	}
	defer f.Close()

	return g.templates.ExecuteTemplate(f, "example.ts.tmpl", map[string]interface{}{
		"Servers": servers,
	})
}

// parseSchema parses a JSON schema into SchemaData
func parseSchema(schemaInterface interface{}) (*SchemaData, error) {
	if schemaInterface == nil {
		return &SchemaData{Properties: []SchemaProperty{}}, nil
	}

	schemaJSON, err := json.Marshal(schemaInterface)
	if err != nil {
		return nil, err
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return nil, err
	}

	properties := extractSchemaProperties(schema)
	return &SchemaData{Properties: properties}, nil
}

// extractSchemaProperties extracts property information from a JSON schema
func extractSchemaProperties(schema map[string]interface{}) []SchemaProperty {
	properties := []SchemaProperty{}

	propsMap, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return properties
	}

	requiredFields := getRequiredFields(schema)

	for name, propData := range propsMap {
		prop, ok := propData.(map[string]interface{})
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

		// Handle array types with object items
		typeStr, _ := prop["type"].(string)
		if typeStr == "array" {
			if items, ok := prop["items"].(map[string]interface{}); ok {
				itemType, _ := items["type"].(string)
				if itemType == "object" {
					// Extract nested properties from array items
					schemaProp.IsArrayOfObjects = true
					schemaProp.ArrayItemProperties = extractSchemaProperties(items)
					schemaProp.TypeScriptType = "Array<object>" // Placeholder, template will handle this
				} else {
					schemaProp.TypeScriptType = jsonTypeToTS(prop)
				}
			} else {
				schemaProp.TypeScriptType = jsonTypeToTS(prop)
			}
		} else {
			schemaProp.TypeScriptType = jsonTypeToTS(prop)
		}

		properties = append(properties, schemaProp)
	}

	return properties
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

func jsonTypeToTS(prop map[string]interface{}) string {
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
		itemsType := "any"
		if items, ok := prop["items"].(map[string]interface{}); ok {
			itemsType = jsonTypeToTS(items)
		}
		return fmt.Sprintf("%s[]", itemsType)
	case "object":
		return "Record<string, any>"
	default:
		return "any"
	}
}

func getRequiredFields(schema map[string]interface{}) []string {
	required := []string{}
	if reqVal, ok := schema["required"]; ok {
		if reqArr, ok := reqVal.([]interface{}); ok {
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
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getArray(m map[string]interface{}, key string) []interface{} {
	if val, ok := m[key]; ok {
		if arr, ok := val.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

func getFloat(m map[string]interface{}, key string) *float64 {
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

func getInt(m map[string]interface{}, key string) *int {
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
