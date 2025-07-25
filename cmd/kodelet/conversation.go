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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// ConversationListConfig holds configuration for the conversation list command
type ConversationListConfig struct {
	StartDate  string
	EndDate    string
	Search     string
	Provider   string
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
		Provider:   "",
		Limit:      0,
		Offset:     0,
		SortBy:     "updated_at",
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

func init() {
	// Add list command flags
	listDefaults := NewConversationListConfig()
	conversationListCmd.Flags().String("start", listDefaults.StartDate, "Filter conversations after this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("end", listDefaults.EndDate, "Filter conversations before this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("search", listDefaults.Search, "Search term to filter conversations")
	conversationListCmd.Flags().String("provider", listDefaults.Provider, "Filter conversations by LLM provider (anthropic or openai)")
	conversationListCmd.Flags().Int("limit", listDefaults.Limit, "Maximum number of conversations to display")
	conversationListCmd.Flags().Int("offset", listDefaults.Offset, "Offset for pagination")
	conversationListCmd.Flags().String("sort-by", listDefaults.SortBy, "Field to sort by: updated_at, created_at, or messages")
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

	// Add subcommands
	conversationCmd.AddCommand(conversationListCmd)
	conversationCmd.AddCommand(conversationDeleteCmd)
	conversationCmd.AddCommand(conversationShowCmd)
	conversationCmd.AddCommand(conversationImportCmd)
	conversationCmd.AddCommand(conversationExportCmd)
	conversationCmd.AddCommand(conversationEditCmd)
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
	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
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
func NewConversationListOutput(summaries []convtypes.ConversationSummary, format OutputFormat) *ConversationListOutput {
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

		// Convert model type to friendly provider name
		provider := summary.Provider
		switch summary.Provider {
		case "anthropic":
			provider = "Anthropic"
		case "openai":
			provider = "OpenAI"
		}

		output.Conversations = append(output.Conversations, ConversationSummaryOutput{
			ID:           summary.ID,
			CreatedAt:    summary.CreatedAt,
			UpdatedAt:    summary.UpdatedAt,
			MessageCount: summary.MessageCount,
			Provider:     provider,
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
		return errors.Wrap(err, "error generating JSON output")
	}

	_, err = fmt.Fprintln(w, string(jsonData))
	return err
}

// renderTable renders the output in table format
func (o *ConversationListOutput) renderTable(w io.Writer) error {
	// Create a tabwriter with padding for better readability
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Print table header
	fmt.Fprintln(tw, "ID\tCreated\tUpdated\tMessages\tProvider\tSummary")
	fmt.Fprintln(tw, "----\t-------\t-------\t--------\t--------\t-------")

	for _, summary := range o.Conversations {
		// Format creation and update dates
		created := summary.CreatedAt.Format(time.RFC3339)
		updated := summary.UpdatedAt.Format(time.RFC3339)

		// Truncate long previews to allow room for provider column
		preview := summary.Preview
		if len(preview) > 50 {
			preview = strings.TrimSpace(preview[:47]) + "..."
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
			summary.ID,
			created,
			updated,
			summary.MessageCount,
			summary.Provider,
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
	Provider     string    `json:"provider"`
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
	options := convtypes.QueryOptions{
		SearchTerm: config.Search,
		Provider:   config.Provider,
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
	result, err := store.Query(ctx, options)
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
	err = store.Delete(ctx, id)
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
	record, err := store.Load(ctx, id)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		os.Exit(1)
	}

	// Extract messages from raw message data
	messages, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.ToolResults)
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
		presenter.Error(errors.Errorf("unsupported format: %s", config.Format), "Unknown format. Supported formats are raw, json, and text")
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
	if _, err := store.Load(ctx, record.ID); err == nil {
		if !config.Force {
			presenter.Error(errors.Errorf("conversation with ID %s already exists", record.ID), "Use --force to overwrite")
			os.Exit(1)
		}
	}

	// Save the conversation
	if err := store.Save(ctx, *record); err != nil {
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
	record, err := store.Load(ctx, conversationID)
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
			presenter.Error(errors.New("cannot use both --gist and --public-gist flags"), "Conflicting flags")
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
		return nil, errors.Wrap(err, "failed to fetch from URL")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// validateConversationRecord validates and parses a conversation record
func validateConversationRecord(data []byte) (*convtypes.ConversationRecord, error) {
	var record convtypes.ConversationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, errors.Wrap(err, "invalid JSON format")
	}

	// Validate required fields
	if record.ID == "" {
		return nil, errors.New("conversation ID is required")
	}

	if record.Provider == "" {
		return nil, errors.New("model type is required")
	}

	// Validate supported providers
	if record.Provider != "anthropic" && record.Provider != "openai" {
		return nil, errors.Errorf("unsupported model type: %s (supported: anthropic, openai)", record.Provider)
	}

	if len(record.RawMessages) == 0 {
		return nil, errors.New("raw messages are required")
	}

	// Validate that messages can be extracted
	if record.ToolResults == nil {
		record.ToolResults = make(map[string]tools.StructuredToolResult)
	}

	_, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.ToolResults)
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract messages")
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
		return errors.Wrap(err, "failed to create temporary file")
	}
	defer os.Remove(tmpFile.Name())

	// Write data to temporary file
	if _, err := tmpFile.Write(jsonData); err != nil {
		tmpFile.Close()
		return errors.Wrap(err, "failed to write to temporary file")
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
		return errors.Wrapf(err, "failed to create gist (output: %s)", string(output))
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
	record, err := store.Load(ctx, conversationID)
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
	if err := store.Save(ctx, *editedRecord); err != nil {
		presenter.Error(err, "Failed to save edited conversation")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s edited successfully", conversationID))
}
