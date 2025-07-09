package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/cobra"
)

// ConversationListConfig holds configuration for the conversation list command
type ConversationListConfig struct {
	StartDate  string
	EndDate    string
	Search     string
	Limit      int
	Offset     int
	SortBy     string
	SortOrder  string
	JSONOutput bool
}

// NewConversationListConfig creates a new ConversationListConfig with default values
func NewConversationListConfig() *ConversationListConfig {
	return &ConversationListConfig{
		StartDate:  "",
		EndDate:    "",
		Search:     "",
		Limit:      0,
		Offset:     0,
		SortBy:     "updated",
		SortOrder:  "desc",
		JSONOutput: false,
	}
}

// ConversationDeleteConfig holds configuration for the conversation delete command
type ConversationDeleteConfig struct {
	NoConfirm bool
}

// NewConversationDeleteConfig creates a new ConversationDeleteConfig with default values
func NewConversationDeleteConfig() *ConversationDeleteConfig {
	return &ConversationDeleteConfig{
		NoConfirm: false,
	}
}

// ConversationShowConfig holds configuration for the conversation show command
type ConversationShowConfig struct {
	Format string
}

// NewConversationShowConfig creates a new ConversationShowConfig with default values
func NewConversationShowConfig() *ConversationShowConfig {
	return &ConversationShowConfig{
		Format: "text",
	}
}

// ConversationImportConfig holds configuration for the conversation import command
type ConversationImportConfig struct {
	Force bool
}

// NewConversationImportConfig creates a new ConversationImportConfig with default values
func NewConversationImportConfig() *ConversationImportConfig {
	return &ConversationImportConfig{
		Force: false,
	}
}

// ConversationExportConfig holds configuration for the conversation export command
type ConversationExportConfig struct {
	UseGist       bool
	UsePublicGist bool
}

// NewConversationExportConfig creates a new ConversationExportConfig with default values
func NewConversationExportConfig() *ConversationExportConfig {
	return &ConversationExportConfig{
		UseGist:       false,
		UsePublicGist: false,
	}
}

// ConversationEditConfig holds configuration for the conversation edit command
type ConversationEditConfig struct {
	Editor   string
	EditArgs string
}

// NewConversationEditConfig creates a new ConversationEditConfig with default values
func NewConversationEditConfig() *ConversationEditConfig {
	return &ConversationEditConfig{
		Editor:   "",
		EditArgs: "",
	}
}

// MigrationConfig holds configuration for the migration command
type MigrationConfig struct {
	DryRun     bool
	Force      bool
	BackupPath string
	Verbose    bool
	JSONPath   string
	DBPath     string
}

// NewMigrationConfig creates a new MigrationConfig with default values
func NewMigrationConfig() *MigrationConfig {
	return &MigrationConfig{
		DryRun:     false,
		Force:      false,
		BackupPath: "",
		Verbose:    false,
		JSONPath:   "",
		DBPath:     "",
	}
}

var conversationCmd = &cobra.Command{
	Use:   "conversation",
	Short: "Manage saved conversations",
	Long:  `List, view, and delete saved conversations.`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

var conversationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved conversations",
	Long:  `List saved conversations with filtering and sorting options.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getConversationListConfigFromFlags(cmd)
		listConversationsCmd(ctx, config)
	},
}

var conversationDeleteCmd = &cobra.Command{
	Use:   "delete [conversationID]",
	Short: "Delete a specific conversation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getConversationDeleteConfigFromFlags(cmd)
		deleteConversationCmd(ctx, args[0], config)
	},
}

var conversationShowCmd = &cobra.Command{
	Use:   "show [conversationID]",
	Short: "Show a specific conversation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getConversationShowConfigFromFlags(cmd)
		showConversationCmd(ctx, args[0], config)
	},
}

var conversationImportCmd = &cobra.Command{
	Use:   "import [path_or_url]",
	Short: "Import a conversation from a file or URL",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getConversationImportConfigFromFlags(cmd)
		importConversationCmd(ctx, args[0], config)
	},
}

var conversationExportCmd = &cobra.Command{
	Use:   "export [conversationID] [path]",
	Short: "Export a conversation to a file or create a gist",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getConversationExportConfigFromFlags(cmd)

		var path string
		if len(args) > 1 {
			path = args[1]
		}

		exportConversationCmd(ctx, args[0], path, config)
	},
}

var conversationEditCmd = &cobra.Command{
	Use:   "edit [conversationID]",
	Short: "Edit a conversation record in JSON format",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getConversationEditConfigFromFlags(cmd)
		editConversationCmd(ctx, args[0], config)
	},
}

var conversationMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate conversations from JSON to BBolt format",
	Long:  `Migrate existing JSON conversations to the new BBolt format for better performance and multi-process support.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getMigrationConfigFromFlags(cmd)
		migrateConversationsCmd(ctx, config)
	},
}

func init() {
	// Add list command flags
	listDefaults := NewConversationListConfig()
	conversationListCmd.Flags().String("start", listDefaults.StartDate, "Filter conversations after this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("end", listDefaults.EndDate, "Filter conversations before this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("search", listDefaults.Search, "Search term to filter conversations")
	conversationListCmd.Flags().Int("limit", listDefaults.Limit, "Maximum number of conversations to display")
	conversationListCmd.Flags().Int("offset", listDefaults.Offset, "Offset for pagination")
	conversationListCmd.Flags().String("sort-by", listDefaults.SortBy, "Field to sort by: updated, created, or messages")
	conversationListCmd.Flags().String("sort-order", listDefaults.SortOrder, "Sort order: asc (ascending) or desc (descending)")
	conversationListCmd.Flags().Bool("json", listDefaults.JSONOutput, "Output in JSON format")

	// Add delete command flags
	deleteDefaults := NewConversationDeleteConfig()
	conversationDeleteCmd.Flags().Bool("no-confirm", deleteDefaults.NoConfirm, "Skip confirmation prompt")

	// Add show command flags
	showDefaults := NewConversationShowConfig()
	conversationShowCmd.Flags().String("format", showDefaults.Format, "Output format: raw, json, or text")

	// Add import command flags
	importDefaults := NewConversationImportConfig()
	conversationImportCmd.Flags().Bool("force", importDefaults.Force, "Force overwrite existing conversation")

	// Add export command flags
	exportDefaults := NewConversationExportConfig()
	conversationExportCmd.Flags().Bool("gist", exportDefaults.UseGist, "Create a private gist using gh command")
	conversationExportCmd.Flags().Bool("public-gist", exportDefaults.UsePublicGist, "Create a public gist using gh command")

	// Add edit command flags
	editDefaults := NewConversationEditConfig()
	conversationEditCmd.Flags().String("editor", editDefaults.Editor, "Editor to use for editing the conversation (default: git config core.editor, then $EDITOR, then vim)")
	conversationEditCmd.Flags().String("edit-args", editDefaults.EditArgs, "Additional arguments to pass to the editor (e.g., '--wait' for VS Code)")

	// Add migrate command flags
	migrateDefaults := NewMigrationConfig()
	conversationMigrateCmd.Flags().Bool("dry-run", migrateDefaults.DryRun, "Show what would be migrated without actually migrating")
	conversationMigrateCmd.Flags().Bool("force", migrateDefaults.Force, "Overwrite existing conversations in target store")
	conversationMigrateCmd.Flags().String("backup-path", migrateDefaults.BackupPath, "Path to backup JSON files (default: ~/.kodelet/backup)")
	conversationMigrateCmd.Flags().Bool("verbose", migrateDefaults.Verbose, "Show detailed migration progress")
	conversationMigrateCmd.Flags().String("json-path", migrateDefaults.JSONPath, "Path to JSON conversations directory (default: ~/.kodelet/conversations)")
	conversationMigrateCmd.Flags().String("db-path", migrateDefaults.DBPath, "Path to BBolt database file (default: ~/.kodelet/storage.db)")

	// Add subcommands
	conversationCmd.AddCommand(conversationListCmd)
	conversationCmd.AddCommand(conversationDeleteCmd)
	conversationCmd.AddCommand(conversationShowCmd)
	conversationCmd.AddCommand(conversationImportCmd)
	conversationCmd.AddCommand(conversationExportCmd)
	conversationCmd.AddCommand(conversationEditCmd)
	conversationCmd.AddCommand(conversationMigrateCmd)
}

// getConversationListConfigFromFlags extracts list configuration from command flags
func getConversationListConfigFromFlags(cmd *cobra.Command) *ConversationListConfig {
	config := NewConversationListConfig()

	if startDate, err := cmd.Flags().GetString("start"); err == nil {
		config.StartDate = startDate
	}
	if endDate, err := cmd.Flags().GetString("end"); err == nil {
		config.EndDate = endDate
	}
	if search, err := cmd.Flags().GetString("search"); err == nil {
		config.Search = search
	}
	if limit, err := cmd.Flags().GetInt("limit"); err == nil {
		config.Limit = limit
	}
	if offset, err := cmd.Flags().GetInt("offset"); err == nil {
		config.Offset = offset
	}
	if sortBy, err := cmd.Flags().GetString("sort-by"); err == nil {
		config.SortBy = sortBy
	}
	if sortOrder, err := cmd.Flags().GetString("sort-order"); err == nil {
		config.SortOrder = sortOrder
	}
	if jsonOutput, err := cmd.Flags().GetBool("json"); err == nil {
		config.JSONOutput = jsonOutput
	}

	return config
}

// getConversationDeleteConfigFromFlags extracts delete configuration from command flags
func getConversationDeleteConfigFromFlags(cmd *cobra.Command) *ConversationDeleteConfig {
	config := NewConversationDeleteConfig()

	if noConfirm, err := cmd.Flags().GetBool("no-confirm"); err == nil {
		config.NoConfirm = noConfirm
	}

	return config
}

// getConversationShowConfigFromFlags extracts show configuration from command flags
func getConversationShowConfigFromFlags(cmd *cobra.Command) *ConversationShowConfig {
	config := NewConversationShowConfig()

	if format, err := cmd.Flags().GetString("format"); err == nil {
		config.Format = format
	}

	return config
}

// getConversationImportConfigFromFlags extracts import configuration from command flags
func getConversationImportConfigFromFlags(cmd *cobra.Command) *ConversationImportConfig {
	config := NewConversationImportConfig()

	if force, err := cmd.Flags().GetBool("force"); err == nil {
		config.Force = force
	}

	return config
}

// getConversationExportConfigFromFlags extracts export configuration from command flags
func getConversationExportConfigFromFlags(cmd *cobra.Command) *ConversationExportConfig {
	config := NewConversationExportConfig()

	if useGist, err := cmd.Flags().GetBool("gist"); err == nil {
		config.UseGist = useGist
	}

	if usePublicGist, err := cmd.Flags().GetBool("public-gist"); err == nil {
		config.UsePublicGist = usePublicGist
	}

	return config
}

// getConversationEditConfigFromFlags extracts edit configuration from command flags
func getConversationEditConfigFromFlags(cmd *cobra.Command) *ConversationEditConfig {
	config := NewConversationEditConfig()

	if editor, err := cmd.Flags().GetString("editor"); err == nil {
		config.Editor = editor
	}

	if editArgs, err := cmd.Flags().GetString("edit-args"); err == nil {
		config.EditArgs = editArgs
	}

	return config
}

// getMigrationConfigFromFlags extracts migration configuration from command flags
func getMigrationConfigFromFlags(cmd *cobra.Command) *MigrationConfig {
	config := NewMigrationConfig()

	if dryRun, err := cmd.Flags().GetBool("dry-run"); err == nil {
		config.DryRun = dryRun
	}
	if force, err := cmd.Flags().GetBool("force"); err == nil {
		config.Force = force
	}
	if backupPath, err := cmd.Flags().GetString("backup-path"); err == nil {
		config.BackupPath = backupPath
	}
	if verbose, err := cmd.Flags().GetBool("verbose"); err == nil {
		config.Verbose = verbose
	}
	if jsonPath, err := cmd.Flags().GetString("json-path"); err == nil {
		config.JSONPath = jsonPath
	}
	if dbPath, err := cmd.Flags().GetString("db-path"); err == nil {
		config.DBPath = dbPath
	}

	return config
}

// OutputFormat defines the format of the output
type OutputFormat int

const (
	TableFormat OutputFormat = iota
	JSONFormat
)

// ConversationListOutput represents the output for conversation list
type ConversationListOutput struct {
	Conversations []ConversationSummaryOutput
	Format        OutputFormat
}

// NewConversationListOutput creates a new ConversationListOutput
func NewConversationListOutput(summaries []conversations.ConversationSummary, format OutputFormat) *ConversationListOutput {
	output := &ConversationListOutput{
		Conversations: make([]ConversationSummaryOutput, 0, len(summaries)),
		Format:        format,
	}

	for _, summary := range summaries {
		// Extract first message or summary
		preview := summary.FirstMessage
		if summary.Summary != "" {
			preview = summary.Summary
		}

		output.Conversations = append(output.Conversations, ConversationSummaryOutput{
			ID:           summary.ID,
			CreatedAt:    summary.CreatedAt,
			UpdatedAt:    summary.UpdatedAt,
			MessageCount: summary.MessageCount,
			Preview:      preview,
		})
	}

	return output
}

// Render formats and renders the conversation list to the specified writer
func (o *ConversationListOutput) Render(w io.Writer) error {
	if o.Format == JSONFormat {
		return o.renderJSON(w)
	}
	return o.renderTable(w)
}

// renderJSON renders the output in JSON format
func (o *ConversationListOutput) renderJSON(w io.Writer) error {
	type jsonOutput struct {
		Conversations []ConversationSummaryOutput `json:"conversations"`
	}

	output := jsonOutput{
		Conversations: o.Conversations,
	}

	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("error generating JSON output: %v", err)
	}

	_, err = fmt.Fprintln(w, string(jsonData))
	return err
}

// renderTable renders the output in table format
func (o *ConversationListOutput) renderTable(w io.Writer) error {
	// Create a tabwriter with padding for better readability
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Print table header
	fmt.Fprintln(tw, "ID\tCreated\tUpdated\tMessages\tSummary")
	fmt.Fprintln(tw, "----\t-------\t-------\t--------\t-------")

	for _, summary := range o.Conversations {
		// Format creation and update dates
		created := summary.CreatedAt.Format(time.RFC3339)
		updated := summary.UpdatedAt.Format(time.RFC3339)

		// Truncate long previews
		preview := summary.Preview
		if len(preview) > 60 {
			preview = strings.TrimSpace(preview[:57]) + "..."
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			summary.ID,
			created,
			updated,
			summary.MessageCount,
			preview,
		)
	}

	return tw.Flush()
}

// ConversationSummaryOutput represents a single conversation summary for output
type ConversationSummaryOutput struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Preview      string    `json:"preview"`
}

// listConversationsCmd displays a list of saved conversations with query options
func listConversationsCmd(ctx context.Context, config *ConversationListConfig) {

	// Create a store
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Prepare query options
	options := conversations.QueryOptions{
		SearchTerm: config.Search,
		Limit:      config.Limit,
		Offset:     config.Offset,
		SortBy:     config.SortBy,
		SortOrder:  config.SortOrder,
	}

	// Parse start date if provided
	if config.StartDate != "" {
		startDate, err := time.Parse("2006-01-02", config.StartDate)
		if err != nil {
			presenter.Error(err, "Invalid start date format. Please use YYYY-MM-DD")
			os.Exit(1)
		}
		options.StartDate = &startDate
	}

	// Parse end date if provided
	if config.EndDate != "" {
		endDate, err := time.Parse("2006-01-02", config.EndDate)
		if err != nil {
			presenter.Error(err, "Invalid end date format. Please use YYYY-MM-DD")
			os.Exit(1)
		}
		// Set to end of day
		endDate = endDate.Add(24*time.Hour - time.Second)
		options.EndDate = &endDate
	}

	// Query conversations with options
	result, err := store.Query(options)
	if err != nil {
		presenter.Error(err, "Failed to list conversations")
		os.Exit(1)
	}

	summaries := result.ConversationSummaries
	if len(summaries) == 0 {
		presenter.Info("No conversations found matching your criteria.")
		return
	}

	// Determine output format
	format := TableFormat
	if config.JSONOutput {
		format = JSONFormat
	}

	// Create and render the output
	output := NewConversationListOutput(summaries, format)
	if err := output.Render(os.Stdout); err != nil {
		presenter.Error(err, "Failed to render conversation list")
		os.Exit(1)
	}
}

// deleteConversationCmd deletes a specific conversation
func deleteConversationCmd(ctx context.Context, id string, config *ConversationDeleteConfig) {

	// Create a store
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// If no-confirm flag is not set, prompt for confirmation
	if !config.NoConfirm {
		response := presenter.Prompt(fmt.Sprintf("Are you sure you want to delete conversation %s?", id), "y", "N")

		if response != "y" && response != "Y" {
			presenter.Info("Deletion cancelled.")
			return
		}
	}

	// Delete the conversation
	err = store.Delete(id)
	if err != nil {
		presenter.Error(err, "Failed to delete conversation")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s deleted successfully", id))
}

// showConversationCmd displays a specific conversation
func showConversationCmd(ctx context.Context, id string, config *ConversationShowConfig) {

	// Create a store
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Load the conversation record
	record, err := store.Load(id)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		os.Exit(1)
	}

	// Extract messages from raw message data
	messages, err := llm.ExtractMessages(record.ModelType, record.RawMessages, record.ToolResults)
	if err != nil {
		presenter.Error(err, "Failed to parse conversation messages")
		os.Exit(1)
	}

	// Render messages according to the format
	switch config.Format {
	case "raw":
		// Output the raw messages as stored
		fmt.Println(string(record.RawMessages))
	case "json":
		// Convert to simpler JSON format and output
		outputJSON, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			presenter.Error(err, "Failed to generate JSON output")
			os.Exit(1)
		}
		fmt.Println(string(outputJSON))
	case "text":
		// Format as readable text with user/assistant prefixes
		displayConversation(messages)
	default:
		presenter.Error(fmt.Errorf("unsupported format: %s", config.Format), "Unknown format. Supported formats are raw, json, and text")
		os.Exit(1)
	}
}

// displayConversation renders the messages in a readable text format
func displayConversation(messages []llmtypes.Message) {
	for i, msg := range messages {
		// Add a separator between messages
		if i > 0 {
			presenter.Separator()
		}

		// Format based on role
		roleLabel := ""
		switch msg.Role {
		case "user":
			roleLabel = "You"
		case "assistant":
			roleLabel = "Assistant"
		default:
			// Capitalize first letter of role
			if len(msg.Role) > 0 {
				roleLabel = strings.ToUpper(msg.Role[:1]) + msg.Role[1:]
			} else {
				roleLabel = msg.Role
			}
		}

		// Output the formatted message with section header
		presenter.Section(roleLabel)
		fmt.Printf("%s\n", msg.Content)
	}
}

// importConversationCmd imports a conversation from a file or URL
func importConversationCmd(ctx context.Context, source string, config *ConversationImportConfig) {
	// Create a store
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Read the conversation data
	data, err := readConversationData(source)
	if err != nil {
		presenter.Error(err, "Failed to read conversation data")
		os.Exit(1)
	}

	// Validate and parse the conversation record
	record, err := validateConversationRecord(data)
	if err != nil {
		presenter.Error(err, "Invalid conversation data")
		os.Exit(1)
	}

	// Check if conversation already exists
	if _, err := store.Load(record.ID); err == nil {
		if !config.Force {
			presenter.Error(fmt.Errorf("conversation with ID %s already exists", record.ID), "Use --force to overwrite")
			os.Exit(1)
		}
	}

	// Save the conversation
	if err := store.Save(*record); err != nil {
		presenter.Error(err, "Failed to save conversation")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s imported successfully", record.ID))
}

// exportConversationCmd exports a conversation to a file or creates a gist
func exportConversationCmd(ctx context.Context, conversationID string, path string, config *ConversationExportConfig) {
	// Create a store
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Load the conversation
	record, err := store.Load(conversationID)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		os.Exit(1)
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		presenter.Error(err, "Failed to serialize conversation")
		os.Exit(1)
	}

	// Handle gist export
	if config.UseGist || config.UsePublicGist {
		// Check for conflicting flags
		if config.UseGist && config.UsePublicGist {
			presenter.Error(fmt.Errorf("cannot use both --gist and --public-gist flags"), "Conflicting flags")
			os.Exit(1)
		}

		isPrivate := config.UseGist // private if --gist, public if --public-gist
		if err := createGist(conversationID, jsonData, isPrivate); err != nil {
			presenter.Error(err, "Failed to create gist")
			os.Exit(1)
		}
		return
	}

	// Handle file export
	if path == "" {
		path = fmt.Sprintf("%s.json", conversationID)
	}

	if err := os.WriteFile(path, jsonData, 0644); err != nil {
		presenter.Error(err, "Failed to write file")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s exported to %s", conversationID, path))
}

// readConversationData reads conversation data from a file or URL
func readConversationData(source string) ([]byte, error) {
	// Check if it's a URL
	if parsedURL, err := url.Parse(source); err == nil && parsedURL.Scheme != "" {
		return readFromURL(source)
	}

	// It's a file path
	return os.ReadFile(source)
}

// readFromURL reads data from a URL
func readFromURL(urlStr string) ([]byte, error) {
	resp, err := http.Get(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// validateConversationRecord validates and parses a conversation record
func validateConversationRecord(data []byte) (*conversations.ConversationRecord, error) {
	var record conversations.ConversationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("invalid JSON format: %w", err)
	}

	// Validate required fields
	if record.ID == "" {
		return nil, fmt.Errorf("conversation ID is required")
	}

	if record.ModelType == "" {
		return nil, fmt.Errorf("model type is required")
	}

	// Validate supported providers
	if record.ModelType != "anthropic" && record.ModelType != "openai" {
		return nil, fmt.Errorf("unsupported model type: %s (supported: anthropic, openai)", record.ModelType)
	}

	if len(record.RawMessages) == 0 {
		return nil, fmt.Errorf("raw messages are required")
	}

	// Validate that messages can be extracted
	if record.ToolResults == nil {
		record.ToolResults = make(map[string]tools.StructuredToolResult)
	}

	_, err := llm.ExtractMessages(record.ModelType, record.RawMessages, record.ToolResults)
	if err != nil {
		return nil, fmt.Errorf("failed to extract messages: %w", err)
	}

	// Set timestamps if not provided
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now()
	}

	return &record, nil
}

// createGist creates a gist using the gh command
func createGist(conversationID string, jsonData []byte, isPrivate bool) error {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("conversation_%s_*.json", conversationID))
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write data to temporary file
	if _, err := tmpFile.Write(jsonData); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write to temporary file: %w", err)
	}
	tmpFile.Close()

	// Build gh command with appropriate visibility flag
	args := []string{"gist", "create"}
	if !isPrivate {
		// Only add --public flag for public gists; private is the default
		args = append(args, "--public")
	}
	args = append(args, "--filename", fmt.Sprintf("conversation_%s.json", conversationID), tmpFile.Name())

	// Create gist using gh command
	cmd := exec.Command("gh", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create gist: %w (output: %s)", err, string(output))
	}

	result := strings.TrimSpace(string(output))
	visibility := "private"
	if !isPrivate {
		visibility = "public"
	}

	presenter.Info(result)
	presenter.Success(fmt.Sprintf("Conversation %s exported to %s gist", conversationID, visibility))
	return nil
}

// editConversationCmd opens a conversation record in JSON format for editing
func editConversationCmd(ctx context.Context, conversationID string, config *ConversationEditConfig) {
	// Create a store
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// Load the conversation
	record, err := store.Load(conversationID)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		os.Exit(1)
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		presenter.Error(err, "Failed to serialize conversation")
		os.Exit(1)
	}

	// Create a temporary file for editing
	tempFile, err := os.CreateTemp("", fmt.Sprintf("conversation_%s_*.json", conversationID))
	if err != nil {
		presenter.Error(err, "Failed to create temporary file")
		os.Exit(1)
	}
	defer os.Remove(tempFile.Name())

	// Write the JSON to the temporary file
	if _, err := tempFile.Write(jsonData); err != nil {
		presenter.Error(err, "Failed to write to temporary file")
		os.Exit(1)
	}
	tempFile.Close()

	// Determine which editor to use
	editor := config.Editor
	if editor == "" {
		editor = getEditor()
	}

	// Parse editor command and arguments
	editorCmd := []string{editor}
	if config.EditArgs != "" {
		args := strings.Fields(config.EditArgs)
		editorCmd = append(editorCmd, args...)
	}
	editorCmd = append(editorCmd, tempFile.Name())

	fmt.Println(editorCmd)

	// Open the file in the editor
	cmd := exec.Command(editorCmd[0], editorCmd[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		presenter.Error(err, "Failed to open editor")
		os.Exit(1)
	}

	// Read the edited content
	editedData, err := os.ReadFile(tempFile.Name())
	if err != nil {
		presenter.Error(err, "Failed to read edited file")
		os.Exit(1)
	}

	// Parse the edited JSON to validate it
	editedRecord, err := validateConversationRecord(editedData)
	if err != nil {
		presenter.Error(err, "Invalid edited conversation data")
		os.Exit(1)
	}

	// Save the edited conversation
	if err := store.Save(*editedRecord); err != nil {
		presenter.Error(err, "Failed to save edited conversation")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s edited successfully", conversationID))
}

// migrateConversationsCmd performs the migration operation
func migrateConversationsCmd(ctx context.Context, config *MigrationConfig) {
	basePath, err := conversations.GetDefaultBasePath()
	if err != nil {
		presenter.Error(err, "Failed to get default base path")
		os.Exit(1)
	}

	// Set default paths if not provided
	if config.JSONPath == "" {
		config.JSONPath = basePath
	}

	if config.DBPath == "" {
		config.DBPath = filepath.Join(basePath, "storage.db")
	}

	if config.BackupPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			presenter.Error(err, "Failed to get home directory")
			os.Exit(1)
		}
		config.BackupPath = filepath.Join(homeDir, ".cache", "kodelet", "backup")
	}

	// Check if there are conversations to migrate
	conversationIDs, err := conversations.DetectJSONConversations(ctx, config.JSONPath)
	if err != nil {
		presenter.Error(err, "Failed to detect JSON conversations")
		os.Exit(1)
	}

	if len(conversationIDs) == 0 {
		presenter.Info("No JSON conversations found to migrate")
		return
	}

	presenter.Info(fmt.Sprintf("Found %d conversations to migrate", len(conversationIDs)))

	// Create backup if not dry run
	if !config.DryRun {
		presenter.Info("Creating backup of JSON conversations...")
		if err := conversations.BackupJSONConversations(ctx, config.JSONPath, config.BackupPath); err != nil {
			presenter.Error(err, "Failed to create backup")
			os.Exit(1)
		}
		presenter.Success(fmt.Sprintf("Backup created at: %s", config.BackupPath))
	}

	// Perform migration
	migrationOptions := conversations.MigrationOptions{
		DryRun:     config.DryRun,
		Force:      config.Force,
		BackupPath: config.BackupPath,
		Verbose:    config.Verbose,
	}

	result, err := conversations.MigrateJSONToBBolt(ctx, config.JSONPath, config.DBPath, migrationOptions)
	if err != nil {
		presenter.Error(err, "Migration failed")
		os.Exit(1)
	}

	// Display results
	if config.DryRun {
		presenter.Info("Dry run completed - no changes made")
	} else {
		presenter.Success("Migration completed successfully")
	}

	presenter.Info(fmt.Sprintf("Total conversations: %d", result.TotalConversations))
	presenter.Info(fmt.Sprintf("Successfully migrated: %d", result.MigratedCount))
	if result.FailedCount > 0 {
		presenter.Warning(fmt.Sprintf("Failed: %d", result.FailedCount))
	}
	if result.SkippedCount > 0 {
		presenter.Info(fmt.Sprintf("Skipped: %d", result.SkippedCount))
	}
	presenter.Info(fmt.Sprintf("Duration: %v", result.Duration))

	if len(result.FailedIDs) > 0 {
		presenter.Warning(fmt.Sprintf("Failed to migrate: %s", strings.Join(result.FailedIDs, ", ")))
	}

	if !config.DryRun && result.MigratedCount > 0 {
		presenter.Info("Your conversations have been migrated to the new BBolt format")
		presenter.Info("You can now use all Kodelet features with improved performance")
		presenter.Info(fmt.Sprintf("Original JSON files have been backed up to: %s", config.BackupPath))
	}
}
