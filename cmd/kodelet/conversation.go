package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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

	// Add subcommands
	conversationCmd.AddCommand(conversationListCmd)
	conversationCmd.AddCommand(conversationDeleteCmd)
	conversationCmd.AddCommand(conversationShowCmd)
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
	logger.G(ctx).WithFields(map[string]interface{}{
		"command":     "list",
		"search_term": config.Search,
		"limit":       config.Limit,
		"offset":      config.Offset,
		"sort_by":     config.SortBy,
		"sort_order":  config.SortOrder,
		"json_output": config.JSONOutput,
	}).Info("Starting conversation list operation")

	// Create a store
	store, err := conversations.GetConversationStore()
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		logger.G(ctx).WithError(err).Error("Failed to get conversation store")
		os.Exit(1)
	}

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
			logger.G(ctx).WithError(err).WithField("start_date", config.StartDate).Error("Failed to parse start date")
			os.Exit(1)
		}
		options.StartDate = &startDate
		logger.G(ctx).WithField("start_date", startDate).Debug("Parsed start date filter")
	}

	// Parse end date if provided
	if config.EndDate != "" {
		endDate, err := time.Parse("2006-01-02", config.EndDate)
		if err != nil {
			presenter.Error(err, "Invalid end date format. Please use YYYY-MM-DD")
			logger.G(ctx).WithError(err).WithField("end_date", config.EndDate).Error("Failed to parse end date")
			os.Exit(1)
		}
		// Set to end of day
		endDate = endDate.Add(24*time.Hour - time.Second)
		options.EndDate = &endDate
		logger.G(ctx).WithField("end_date", endDate).Debug("Parsed end date filter")
	}

	// Query conversations with options
	summaries, err := store.Query(options)
	if err != nil {
		presenter.Error(err, "Failed to list conversations")
		logger.G(ctx).WithError(err).Error("Failed to query conversations from store")
		os.Exit(1)
	}

	logger.G(ctx).WithField("conversation_count", len(summaries)).Info("Retrieved conversations from store")

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
		logger.G(ctx).WithError(err).Error("Failed to render output")
		os.Exit(1)
	}

	logger.G(ctx).WithField("format", format).Info("Successfully rendered conversation list")
}

// deleteConversationCmd deletes a specific conversation
func deleteConversationCmd(ctx context.Context, id string, config *ConversationDeleteConfig) {
	logger.G(ctx).WithFields(map[string]interface{}{
		"command":         "delete",
		"conversation_id": id,
		"no_confirm":      config.NoConfirm,
	}).Info("Starting conversation delete operation")

	// Create a store
	store, err := conversations.GetConversationStore()
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		logger.G(ctx).WithError(err).Error("Failed to get conversation store")
		os.Exit(1)
	}

	// If no-confirm flag is not set, prompt for confirmation
	if !config.NoConfirm {
		response := presenter.Prompt(fmt.Sprintf("Are you sure you want to delete conversation %s?", id), "y", "N")
		logger.G(ctx).WithField("user_response", response).Debug("User confirmation prompt response")

		if response != "y" && response != "Y" {
			presenter.Info("Deletion cancelled.")
			logger.G(ctx).Info("User cancelled conversation deletion")
			return
		}
	}

	// Delete the conversation
	err = store.Delete(id)
	if err != nil {
		presenter.Error(err, "Failed to delete conversation")
		logger.G(ctx).WithError(err).WithField("conversation_id", id).Error("Failed to delete conversation from store")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s deleted successfully", id))
	logger.G(ctx).WithField("conversation_id", id).Info("Successfully deleted conversation")
}

// showConversationCmd displays a specific conversation
func showConversationCmd(ctx context.Context, id string, config *ConversationShowConfig) {
	logger.G(ctx).WithFields(map[string]interface{}{
		"command":         "show",
		"conversation_id": id,
		"format":          config.Format,
	}).Info("Starting conversation show operation")

	// Create a store
	store, err := conversations.GetConversationStore()
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		logger.G(ctx).WithError(err).Error("Failed to get conversation store")
		os.Exit(1)
	}

	// Load the conversation record
	record, err := store.Load(id)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		logger.G(ctx).WithError(err).WithField("conversation_id", id).Error("Failed to load conversation from store")
		os.Exit(1)
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"conversation_id": id,
		"model_type":      record.ModelType,
		"message_length":  len(record.RawMessages),
	}).Info("Successfully loaded conversation record")

	// Extract messages from raw message data
	messages, err := llm.ExtractMessages(record.ModelType, record.RawMessages)
	if err != nil {
		presenter.Error(err, "Failed to parse conversation messages")
		logger.G(ctx).WithError(err).WithField("conversation_id", id).Error("Failed to extract messages from raw data")
		os.Exit(1)
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"conversation_id": id,
		"message_count":   len(messages),
	}).Debug("Successfully extracted messages")

	// Render messages according to the format
	switch config.Format {
	case "raw":
		// Output the raw messages as stored
		fmt.Println(string(record.RawMessages))
		logger.G(ctx).WithField("format", "raw").Debug("Rendered conversation in raw format")
	case "json":
		// Convert to simpler JSON format and output
		outputJSON, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			presenter.Error(err, "Failed to generate JSON output")
			logger.G(ctx).WithError(err).Error("Failed to marshal messages to JSON")
			os.Exit(1)
		}
		fmt.Println(string(outputJSON))
		logger.G(ctx).WithField("format", "json").Debug("Rendered conversation in JSON format")
	case "text":
		// Format as readable text with user/assistant prefixes
		displayConversation(messages)
		logger.G(ctx).WithField("format", "text").Debug("Rendered conversation in text format")
	default:
		presenter.Error(fmt.Errorf("unsupported format: %s", config.Format), "Unknown format. Supported formats are raw, json, and text")
		logger.G(ctx).WithField("format", config.Format).Error("Unsupported output format specified")
		os.Exit(1)
	}

	logger.G(ctx).WithFields(map[string]interface{}{
		"conversation_id": id,
		"format":          config.Format,
	}).Info("Successfully displayed conversation")
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
