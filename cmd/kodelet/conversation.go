package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/session"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

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

func NewConversationListConfig() *ConversationListConfig {
	return &ConversationListConfig{
		StartDate:  "",
		EndDate:    "",
		Search:     "",
		Provider:   "",
		Limit:      10,
		Offset:     0,
		SortBy:     "updated_at",
		SortOrder:  "desc",
		JSONOutput: false,
	}
}

type ConversationDeleteConfig struct {
	NoConfirm bool
}

func NewConversationDeleteConfig() *ConversationDeleteConfig {
	return &ConversationDeleteConfig{
		NoConfirm: false,
	}
}

type ConversationShowConfig struct {
	Format    string
	NoHeader  bool
	StatsOnly bool
}

func NewConversationShowConfig() *ConversationShowConfig {
	return &ConversationShowConfig{
		Format:    "text",
		NoHeader:  false,
		StatsOnly: false,
	}
}

type ConversationImportConfig struct {
	Force bool
}

func NewConversationImportConfig() *ConversationImportConfig {
	return &ConversationImportConfig{
		Force: false,
	}
}

type ConversationExportConfig struct {
	UseGist       bool
	UsePublicGist bool
}

func NewConversationExportConfig() *ConversationExportConfig {
	return &ConversationExportConfig{
		UseGist:       false,
		UsePublicGist: false,
	}
}

type ConversationEditConfig struct {
	Editor   string
	EditArgs string
}

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
	Run: func(cmd *cobra.Command, _ []string) {
		cmd.Help()
	},
}

var conversationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved conversations",
	Long:  `List saved conversations with filtering and sorting options.`,
	Run: func(cmd *cobra.Command, _ []string) {
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

type ConversationStreamConfig struct {
	IncludeHistory bool
	HistoryOnly    bool
}

func NewConversationStreamConfig() *ConversationStreamConfig {
	return &ConversationStreamConfig{
		IncludeHistory: false,
		HistoryOnly:    false,
	}
}

var conversationStreamCmd = &cobra.Command{
	Use:   "stream [conversationID]",
	Short: "Stream conversation updates in structured JSON format",
	Long:  "Stream conversation entries in real-time. Use --include-history to show historical data first, then stream new entries (like tail -f). All output is JSON - use jq for filtering and analysis.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		config := getConversationStreamConfigFromFlags(cmd)
		streamConversationCmd(ctx, args[0], config)
	},
}

var conversationForkCmd = &cobra.Command{
	Use:   "fork [conversationID]",
	Short: "Fork a conversation to create a copy with reset usage statistics",
	Long:  "Fork a conversation by copying its messages and context while resetting usage statistics (tokens and costs). If no conversation ID is provided, the most recent conversation will be forked.",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		conversationID := ""
		if len(args) > 0 {
			conversationID = args[0]
		}
		forkConversationCmd(ctx, conversationID)
	},
}

func init() {
	listDefaults := NewConversationListConfig()
	conversationListCmd.Flags().String("start", listDefaults.StartDate, "Filter conversations after this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("end", listDefaults.EndDate, "Filter conversations before this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("search", listDefaults.Search, "Search term to filter conversations")
	conversationListCmd.Flags().String("provider", listDefaults.Provider, "Filter conversations by LLM provider (anthropic, openai, google)")
	conversationListCmd.Flags().Int("limit", listDefaults.Limit, "Maximum number of conversations to display")
	conversationListCmd.Flags().Int("offset", listDefaults.Offset, "Offset for pagination")
	conversationListCmd.Flags().String("sort-by", listDefaults.SortBy, "Field to sort by: updated_at, created_at, or messages")
	conversationListCmd.Flags().String("sort-order", listDefaults.SortOrder, "Sort order: asc (ascending) or desc (descending)")
	conversationListCmd.Flags().Bool("json", listDefaults.JSONOutput, "Output in JSON format")

	deleteDefaults := NewConversationDeleteConfig()
	conversationDeleteCmd.Flags().Bool("no-confirm", deleteDefaults.NoConfirm, "Skip confirmation prompt")

	showDefaults := NewConversationShowConfig()
	conversationShowCmd.Flags().String("format", showDefaults.Format, "Output format: raw, json, text, or markdown")
	conversationShowCmd.Flags().Bool("no-header", showDefaults.NoHeader, "Skip header (stats/summary), show only messages")
	conversationShowCmd.Flags().Bool("stats-only", showDefaults.StatsOnly, "Show only stats/summary without messages")

	importDefaults := NewConversationImportConfig()
	conversationImportCmd.Flags().Bool("force", importDefaults.Force, "Force overwrite existing conversation")

	exportDefaults := NewConversationExportConfig()
	conversationExportCmd.Flags().Bool("gist", exportDefaults.UseGist, "Create a private gist using gh command")
	conversationExportCmd.Flags().Bool("public-gist", exportDefaults.UsePublicGist, "Create a public gist using gh command")

	editDefaults := NewConversationEditConfig()
	conversationEditCmd.Flags().String("editor", editDefaults.Editor, "Editor to use for editing the conversation (default: git config core.editor, then $EDITOR, then vim)")
	conversationEditCmd.Flags().String("edit-args", editDefaults.EditArgs, "Additional arguments to pass to the editor (e.g., '--wait' for VS Code)")

	streamDefaults := NewConversationStreamConfig()
	conversationStreamCmd.Flags().Bool("include-history", streamDefaults.IncludeHistory, "Include historical conversation data before streaming new entries")
	conversationStreamCmd.Flags().Bool("history-only", streamDefaults.HistoryOnly, "Output historical conversation data and exit (no live streaming)")
	conversationStreamCmd.MarkFlagsMutuallyExclusive("include-history", "history-only")

	conversationCmd.AddCommand(conversationListCmd)
	conversationCmd.AddCommand(conversationDeleteCmd)
	conversationCmd.AddCommand(conversationShowCmd)
	conversationCmd.AddCommand(conversationImportCmd)
	conversationCmd.AddCommand(conversationExportCmd)
	conversationCmd.AddCommand(conversationEditCmd)
	conversationCmd.AddCommand(conversationStreamCmd)
	conversationCmd.AddCommand(conversationForkCmd)
}

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

func getConversationDeleteConfigFromFlags(cmd *cobra.Command) *ConversationDeleteConfig {
	config := NewConversationDeleteConfig()

	if noConfirm, err := cmd.Flags().GetBool("no-confirm"); err == nil {
		config.NoConfirm = noConfirm
	}

	return config
}

func getConversationShowConfigFromFlags(cmd *cobra.Command) *ConversationShowConfig {
	config := NewConversationShowConfig()

	if format, err := cmd.Flags().GetString("format"); err == nil {
		config.Format = format
	}
	if noHeader, err := cmd.Flags().GetBool("no-header"); err == nil {
		config.NoHeader = noHeader
	}
	if statsOnly, err := cmd.Flags().GetBool("stats-only"); err == nil {
		config.StatsOnly = statsOnly
	}

	return config
}

func getConversationImportConfigFromFlags(cmd *cobra.Command) *ConversationImportConfig {
	config := NewConversationImportConfig()

	if force, err := cmd.Flags().GetBool("force"); err == nil {
		config.Force = force
	}

	return config
}

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

func getConversationStreamConfigFromFlags(cmd *cobra.Command) *ConversationStreamConfig {
	config := NewConversationStreamConfig()

	if includeHistory, err := cmd.Flags().GetBool("include-history"); err == nil {
		config.IncludeHistory = includeHistory
	}
	if historyOnly, err := cmd.Flags().GetBool("history-only"); err == nil {
		config.HistoryOnly = historyOnly
	}

	return config
}

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

type OutputFormat int

const (
	TableFormat OutputFormat = iota
	JSONFormat
)

type ConversationListOutput struct {
	Conversations []ConversationSummaryOutput
	Format        OutputFormat
}

func NewConversationListOutput(summaries []convtypes.ConversationSummary, metadataByID map[string]map[string]any, format OutputFormat) *ConversationListOutput {
	output := &ConversationListOutput{
		Conversations: make([]ConversationSummaryOutput, 0, len(summaries)),
		Format:        format,
	}

	for _, summary := range summaries {
		preview := summary.FirstMessage
		if summary.Summary != "" {
			preview = summary.Summary
		}

		preview = strings.ReplaceAll(preview, "\n", " ")
		preview = strings.ReplaceAll(preview, "\r", " ")

		metadata := metadataByID[summary.ID]
		platform, apiMode := extractProviderMetadata(summary.Provider, metadata)

		output.Conversations = append(output.Conversations, ConversationSummaryOutput{
			ID:             summary.ID,
			CreatedAt:      summary.CreatedAt,
			UpdatedAt:      summary.UpdatedAt,
			MessageCount:   summary.MessageCount,
			Provider:       displayProviderName(summary.Provider),
			Platform:       platform,
			APIMode:        apiMode,
			Preview:        preview,
			TotalCost:      summary.Usage.TotalCost(),
			CurrentContext: summary.Usage.CurrentContextWindow,
			MaxContext:     summary.Usage.MaxContextWindow,
		})
	}

	return output
}

func normalizeProviderMetadataString(value any) string {
	strValue, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(strValue))
}

func extractProviderMetadata(provider string, metadata map[string]any) (string, string) {
	normalizedProvider := strings.TrimSpace(strings.ToLower(provider))

	platform := ""
	apiMode := ""
	if metadata != nil {
		if platformValue, exists := metadata["platform"]; exists {
			platform = normalizeProviderMetadataString(platformValue)
		}
		if modeValue, exists := metadata["api_mode"]; exists {
			apiMode = normalizeProviderMetadataString(modeValue)
		}
	}

	switch apiMode {
	case "responses_api", "response":
		apiMode = "responses"
	case "chat", "chatcompletions":
		apiMode = "chat_completions"
	}

	if normalizedProvider == "openai-responses" && apiMode == "" {
		apiMode = "responses"
	}

	return platform, apiMode
}

func displayProviderName(provider string) string {
	switch strings.TrimSpace(strings.ToLower(provider)) {
	case "anthropic":
		return "Anthropic"
	case "openai", "openai-responses":
		return "OpenAI"
	case "google":
		return "Google"
	default:
		return provider
	}
}

func (o *ConversationListOutput) Render(w io.Writer) error {
	if o.Format == JSONFormat {
		return o.renderJSON(w)
	}
	return o.renderTable(w)
}

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

func (o *ConversationListOutput) renderTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "ID\tCreated\tUpdated\tMessages\tProvider\tPlatform\tAPI Mode\tCost\tContext\tSummary")
	fmt.Fprintln(tw, "----\t-------\t-------\t--------\t--------\t--------\t--------\t----\t-------\t-------")

	for _, summary := range o.Conversations {
		created := summary.CreatedAt.Format(time.RFC3339)
		updated := summary.UpdatedAt.Format(time.RFC3339)

		// Format cost as dollars with 4 decimal places
		costStr := fmt.Sprintf("$%.4f", summary.TotalCost)

		// Format context window usage
		var contextStr string
		if summary.MaxContext > 0 {
			contextStr = fmt.Sprintf("%d/%d", summary.CurrentContext, summary.MaxContext)
		} else {
			contextStr = "-"
		}

		// Truncate long previews to allow room for other columns
		preview := summary.Preview
		if len(preview) > 50 {
			preview = strings.TrimSpace(preview[:47]) + "..."
		}

		platform := summary.Platform
		if platform == "" {
			platform = "-"
		}
		apiMode := summary.APIMode
		if apiMode == "" {
			apiMode = "-"
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			summary.ID,
			created,
			updated,
			summary.MessageCount,
			summary.Provider,
			platform,
			apiMode,
			costStr,
			contextStr,
			preview,
		)
	}

	return tw.Flush()
}

type ConversationSummaryOutput struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	MessageCount   int       `json:"message_count"`
	Provider       string    `json:"provider"`
	Platform       string    `json:"platform,omitempty"`
	APIMode        string    `json:"api_mode,omitempty"`
	Preview        string    `json:"preview"`
	TotalCost      float64   `json:"total_cost"`
	CurrentContext int       `json:"current_context_window"`
	MaxContext     int       `json:"max_context_window"`
}

func listConversationsCmd(ctx context.Context, config *ConversationListConfig) {
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	options := convtypes.QueryOptions{
		SearchTerm: config.Search,
		Provider:   config.Provider,
		Limit:      config.Limit,
		Offset:     config.Offset,
		SortBy:     config.SortBy,
		SortOrder:  config.SortOrder,
	}

	if config.StartDate != "" {
		startDate, err := time.Parse("2006-01-02", config.StartDate)
		if err != nil {
			presenter.Error(err, "Invalid start date format. Please use YYYY-MM-DD")
			os.Exit(1)
		}
		options.StartDate = &startDate
	}

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

	metadataByID := make(map[string]map[string]any, len(summaries))
	for _, summary := range summaries {
		metadataByID[summary.ID] = summary.Metadata
	}

	format := TableFormat
	if config.JSONOutput {
		format = JSONFormat
	}
	output := NewConversationListOutput(summaries, metadataByID, format)
	if err := output.Render(os.Stdout); err != nil {
		presenter.Error(err, "Failed to render conversation list")
		os.Exit(1)
	}
}

func deleteConversationCmd(ctx context.Context, id string, config *ConversationDeleteConfig) {
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	if !config.NoConfirm {
		response := presenter.Prompt(fmt.Sprintf("Are you sure you want to delete conversation %s?", id), "y", "N")

		if response != "y" && response != "Y" {
			presenter.Info("Deletion cancelled.")
			return
		}
	}
	err = store.Delete(ctx, id)
	if err != nil {
		presenter.Error(err, "Failed to delete conversation")
		os.Exit(1)
	}

	// Also clean up ACP session data if it exists
	if storage, err := session.NewStorage(ctx); err == nil {
		if err := storage.Delete(acptypes.SessionID(id)); err != nil {
			logger.G(ctx).WithError(err).Debug("Failed to delete ACP session data")
		}
		storage.Close()
	}

	presenter.Success(fmt.Sprintf("Conversation %s deleted successfully", id))
}

type ConversationShowOutput struct {
	ID        string             `json:"id"`
	Provider  string             `json:"provider"`
	Platform  string             `json:"platform,omitempty"`
	APIMode   string             `json:"api_mode,omitempty"`
	Summary   string             `json:"summary,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Usage     llmtypes.Usage     `json:"usage"`
	Messages  []llmtypes.Message `json:"messages,omitempty"`
}

func showConversationCmd(ctx context.Context, id string, config *ConversationShowConfig) {
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	record, err := store.Load(ctx, id)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		os.Exit(1)
	}

	platform, apiMode := extractProviderMetadata(record.Provider, record.Metadata)
	providerDisplay := displayProviderName(record.Provider)

	switch config.Format {
	case "raw":
		outputJSON, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			presenter.Error(err, "Failed to generate JSON output")
			os.Exit(1)
		}
		fmt.Println(string(outputJSON))
	case "json":
		output := ConversationShowOutput{
			ID:        record.ID,
			Provider:  providerDisplay,
			Platform:  platform,
			APIMode:   apiMode,
			Summary:   record.Summary,
			CreatedAt: record.CreatedAt,
			UpdatedAt: record.UpdatedAt,
			Usage:     record.Usage,
		}
		if !config.StatsOnly {
			messages, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.Metadata, record.ToolResults)
			if err != nil {
				presenter.Error(err, "Failed to parse conversation messages")
				os.Exit(1)
			}
			output.Messages = messages
		}
		if config.NoHeader {
			outputJSON, err := json.MarshalIndent(output.Messages, "", "  ")
			if err != nil {
				presenter.Error(err, "Failed to generate JSON output")
				os.Exit(1)
			}
			fmt.Println(string(outputJSON))
		} else {
			outputJSON, err := json.MarshalIndent(output, "", "  ")
			if err != nil {
				presenter.Error(err, "Failed to generate JSON output")
				os.Exit(1)
			}
			fmt.Println(string(outputJSON))
		}
	case "text":
		showHeader := !config.NoHeader
		showMessages := !config.StatsOnly
		if showHeader {
			displayConversationHeader(record, providerDisplay, platform, apiMode)
			if showMessages {
				fmt.Println()
			}
		}
		if showMessages {
			messages, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.Metadata, record.ToolResults)
			if err != nil {
				presenter.Error(err, "Failed to parse conversation messages")
				os.Exit(1)
			}
			displayConversation(messages)
		}
	case "markdown":
		showHeader := !config.NoHeader
		showMessages := !config.StatsOnly
		if showHeader {
			fmt.Print(renderConversationHeaderMarkdown(record, providerDisplay, platform, apiMode))
			if showMessages {
				fmt.Println()
			}
		}
		if showMessages {
			messages, err := llm.ExtractConversationEntries(record.Provider, record.RawMessages, record.Metadata, record.ToolResults)
			if err != nil {
				presenter.Error(err, "Failed to parse conversation messages")
				os.Exit(1)
			}
			fmt.Print(renderConversationMarkdown(messages, record.ToolResults))
		}
	default:
		presenter.Error(errors.Errorf("unsupported format: %s", config.Format), "Unknown format. Supported formats are raw, json, text, and markdown")
		os.Exit(1)
	}
}

func displayConversationHeader(record convtypes.ConversationRecord, providerDisplay string, platform string, apiMode string) {
	presenter.Section("Conversation Info")
	fmt.Printf("ID:        %s\n", record.ID)
	fmt.Printf("Provider:  %s\n", providerDisplay)
	if platform != "" {
		fmt.Printf("Platform:  %s\n", platform)
	}
	if apiMode != "" {
		fmt.Printf("API Mode:  %s\n", apiMode)
	}
	fmt.Printf("Created:   %s\n", record.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:   %s\n", record.UpdatedAt.Format(time.RFC3339))

	if record.Summary != "" {
		fmt.Printf("Summary:   %s\n", record.Summary)
	}

	usage := record.Usage
	fmt.Println()
	presenter.Section("Usage Stats")
	fmt.Printf("Input Tokens:   %d\n", usage.InputTokens)
	fmt.Printf("Output Tokens:  %d\n", usage.OutputTokens)
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		fmt.Printf("Cache Read:     %d\n", usage.CacheReadInputTokens)
		fmt.Printf("Cache Creation: %d\n", usage.CacheCreationInputTokens)
	}
	fmt.Printf("Total Cost:     $%.4f\n", usage.TotalCost())
	if usage.MaxContextWindow > 0 {
		fmt.Printf("Context Window: %d / %d\n", usage.CurrentContextWindow, usage.MaxContextWindow)
	}
}

func displayConversation(messages []llmtypes.Message) {
	for i, msg := range messages {
		if i > 0 {
			presenter.Separator()
		}

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
		presenter.Section(roleLabel)
		fmt.Printf("%s\n", msg.Content)
	}
}

func renderConversationHeaderMarkdown(record convtypes.ConversationRecord, providerDisplay string, platform string, apiMode string) string {
	var output strings.Builder

	output.WriteString("# Conversation\n\n")
	output.WriteString("## Info\n\n")
	fmt.Fprintf(&output, "- **ID:** %s\n", inlineMarkdownCode(record.ID))
	fmt.Fprintf(&output, "- **Provider:** %s\n", providerDisplay)
	if platform != "" {
		fmt.Fprintf(&output, "- **Platform:** %s\n", inlineMarkdownCode(platform))
	}
	if apiMode != "" {
		fmt.Fprintf(&output, "- **API Mode:** %s\n", inlineMarkdownCode(apiMode))
	}
	fmt.Fprintf(&output, "- **Created:** %s\n", inlineMarkdownCode(record.CreatedAt.Format(time.RFC3339)))
	fmt.Fprintf(&output, "- **Updated:** %s\n", inlineMarkdownCode(record.UpdatedAt.Format(time.RFC3339)))
	if record.Summary != "" {
		fmt.Fprintf(&output, "- **Summary:** %s\n", sanitizeMarkdownText(record.Summary))
	}

	usage := record.Usage
	output.WriteString("\n## Usage\n\n")
	fmt.Fprintf(&output, "- **Input Tokens:** %d\n", usage.InputTokens)
	fmt.Fprintf(&output, "- **Output Tokens:** %d\n", usage.OutputTokens)
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		fmt.Fprintf(&output, "- **Cache Read:** %d\n", usage.CacheReadInputTokens)
		fmt.Fprintf(&output, "- **Cache Creation:** %d\n", usage.CacheCreationInputTokens)
	}
	fmt.Fprintf(&output, "- **Total Cost:** $%.4f\n", usage.TotalCost())
	if usage.MaxContextWindow > 0 {
		fmt.Fprintf(&output, "- **Context Window:** %d / %d\n", usage.CurrentContextWindow, usage.MaxContextWindow)
	}

	return output.String()
}

func renderConversationMarkdown(messages []conversations.StreamableMessage, toolResults map[string]tools.StructuredToolResult) string {
	var output strings.Builder
	output.WriteString("## Messages\n\n")

	registry := renderers.NewRendererRegistry()
	consumedResults := make(map[int]struct{})

	for i, msg := range messages {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}

		if i > 0 {
			output.WriteString("\n")
		}

		if msg.Kind == "tool-use" {
			resultIndex, resultMsg, hasResult := findMatchingToolResult(messages, i, consumedResults)
			fmt.Fprintf(&output, "### %s\n\n", markdownRoleHeading(msg.Role, "tool-invocation"))
			output.WriteString(renderToolInvocationMarkdown(msg, resultMsg, hasResult, toolResults, registry))
			if hasResult {
				consumedResults[resultIndex] = struct{}{}
			}
			continue
		}

		heading := markdownRoleHeading(msg.Role, msg.Kind)
		fmt.Fprintf(&output, "### %s\n\n", heading)

		switch msg.Kind {
		case "text":
			output.WriteString(renderTextBlockMarkdown(msg))
		case "thinking":
			output.WriteString("<details>\n<summary>Thinking</summary>\n\n")
			output.WriteString(markdownCodeFence("text", msg.Content))
			output.WriteString("\n\n</details>")
		case "tool-result":
			output.WriteString(renderToolResultMarkdown(msg, toolResults, registry))
		default:
			output.WriteString(renderTextBlockMarkdown(msg))
		}
	}

	if len(messages) == 0 {
		output.WriteString("_No messages._\n")
	}

	return strings.TrimRight(output.String(), "\n") + "\n"
}

func markdownRoleHeading(role string, kind string) string {
	base := "Message"
	switch role {
	case "user":
		base = "User"
	case "assistant":
		base = "Assistant"
	case "system":
		base = "System"
	default:
		if role != "" {
			base = strings.ToUpper(role[:1]) + role[1:]
		}
	}

	switch kind {
	case "thinking":
		return base + " · Thinking"
	case "tool-invocation":
		return base + " · Tool"
	case "tool-use":
		return base + " · Tool Call"
	case "tool-result":
		return base + " · Tool Result"
	default:
		return base
	}
}

func renderTextBlockMarkdown(msg conversations.StreamableMessage) string {
	if rendered := renderOpenAIResponsesRawItemMarkdown(msg.RawItem); strings.TrimSpace(rendered) != "" {
		return rendered
	}

	trimmed := strings.TrimSpace(msg.Content)
	if trimmed == "" {
		return "_Empty message._"
	}
	return msg.Content
}

func renderOpenAIResponsesRawItemMarkdown(rawItem json.RawMessage) string {
	if len(rawItem) == 0 {
		return ""
	}

	var rawMessage struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(rawItem, &rawMessage); err != nil || len(rawMessage.Content) == 0 {
		return ""
	}

	var textContent string
	if err := json.Unmarshal(rawMessage.Content, &textContent); err == nil {
		return textContent
	}

	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL string `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(rawMessage.Content, &parts); err != nil {
		return ""
	}

	renderedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			if strings.TrimSpace(part.Text) != "" {
				renderedParts = append(renderedParts, part.Text)
			}
		case "input_image":
			if imageMarkdown := renderInputImageMarkdown(part.ImageURL); imageMarkdown != "" {
				renderedParts = append(renderedParts, imageMarkdown)
			}
		}
	}

	return strings.Join(renderedParts, "\n\n")
}

func renderInputImageMarkdown(imageURL string) string {
	if imageURL == "" {
		return ""
	}

	if strings.HasPrefix(imageURL, "data:") {
		mediaType := mediaTypeFromDataURL(imageURL)
		if mediaType == "" {
			return "_Inline image input._"
		}
		return fmt.Sprintf("_Inline image input (%s)._", mediaType)
	}

	return fmt.Sprintf("Image input: <%s>", imageURL)
}

func mediaTypeFromDataURL(dataURL string) string {
	if !strings.HasPrefix(dataURL, "data:") {
		return ""
	}

	metadata, _, found := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
	if !found {
		return ""
	}

	mediaType, _, _ := strings.Cut(metadata, ";")
	return mediaType
}

func renderToolInvocationMarkdown(
	toolUse conversations.StreamableMessage,
	toolResult conversations.StreamableMessage,
	hasResult bool,
	toolResults map[string]tools.StructuredToolResult,
	registry *renderers.RendererRegistry,
) string {
	var output strings.Builder
	toolName := toolUse.ToolName
	if toolName == "" && hasResult {
		toolName = inferToolNameFromResult(toolResult, toolResults)
	}

	if toolName != "" {
		fmt.Fprintf(&output, "- **Tool:** %s\n", inlineMarkdownCode(toolName))
	}
	if toolUse.ToolCallID != "" {
		fmt.Fprintf(&output, "- **Call ID:** %s\n", inlineMarkdownCode(toolUse.ToolCallID))
	}

	renderedInput := renderToolInputMarkdown(toolUse.ToolName, toolUse.Input)
	if strings.TrimSpace(renderedInput) != "" {
		output.WriteString("\n")
		output.WriteString(renderedInput)
	}

	if hasResult {
		resultBody := renderMergedToolResultMarkdown(toolUse, toolResult, toolResults, registry)
		if strings.TrimSpace(resultBody) != "" {
			output.WriteString("\n\n**Result**\n\n")
			output.WriteString(resultBody)
		}
	}

	return strings.TrimSpace(output.String())
}

func renderMergedToolResultMarkdown(
	toolUse conversations.StreamableMessage,
	toolResult conversations.StreamableMessage,
	toolResults map[string]tools.StructuredToolResult,
	registry *renderers.RendererRegistry,
) string {
	structuredResult, ok := lookupStructuredToolResult(toolResult, toolResults)
	if !ok {
		trimmed := strings.TrimSpace(toolResult.Content)
		if trimmed == "" {
			return ""
		}
		language := "text"
		if json.Valid([]byte(trimmed)) {
			language = "json"
			trimmed = trimJSONForMarkdown(trimmed)
		}
		return markdownCodeFence(language, trimmed)
	}

	if structuredResult.ToolName == "bash" {
		return renderMergedBashResultMarkdown(toolUse, structuredResult)
	}

	resultBody := registry.RenderMarkdown(structuredResult)
	return stripLeadingMarkdownMetadata(resultBody, map[string]struct{}{
		"Tool":    {},
		"Call ID": {},
	})
}

func renderMergedBashResultMarkdown(toolUse conversations.StreamableMessage, result tools.StructuredToolResult) string {
	var meta tools.BashMetadata
	if !tools.ExtractMetadata(result.Metadata, &meta) {
		return stripLeadingMarkdownMetadata(renderers.NewRendererRegistry().RenderMarkdown(result), map[string]struct{}{
			"Tool":    {},
			"Call ID": {},
			"Command": {},
		})
	}

	var output strings.Builder
	status := "success"
	if !result.Success {
		status = "failed"
	}
	fmt.Fprintf(&output, "- **Status:** %s\n", status)
	fmt.Fprintf(&output, "- **Exit code:** %d\n", meta.ExitCode)
	if meta.WorkingDir != "" {
		fmt.Fprintf(&output, "- **Working directory:** %s\n", inlineMarkdownCode(meta.WorkingDir))
	}
	fmt.Fprintf(&output, "- **Execution time:** %s\n", inlineMarkdownCode(meta.ExecutionTime.String()))
	if result.Error != "" {
		fmt.Fprintf(&output, "- **Error:** %s\n", inlineMarkdownCode(result.Error))
	}

	if strings.TrimSpace(meta.Output) != "" {
		output.WriteString("\n**Output**\n\n")
		output.WriteString(markdownCodeFence("text", meta.Output))
	}

	_ = toolUse
	return strings.TrimSpace(output.String())
}

func stripLeadingMarkdownMetadata(input string, keys map[string]struct{}) string {
	lines := strings.Split(input, "\n")
	trimmed := make([]string, 0, len(lines))
	stripping := true

	for _, line := range lines {
		if !stripping {
			trimmed = append(trimmed, line)
			continue
		}

		lineTrimmed := strings.TrimSpace(line)
		if lineTrimmed == "" {
			continue
		}
		if !strings.HasPrefix(lineTrimmed, "- **") {
			stripping = false
			trimmed = append(trimmed, line)
			continue
		}

		key, ok := parseMarkdownMetadataKey(lineTrimmed)
		if !ok {
			stripping = false
			trimmed = append(trimmed, line)
			continue
		}
		if _, skip := keys[key]; skip {
			continue
		}
		trimmed = append(trimmed, line)
	}

	return strings.TrimSpace(strings.Join(trimmed, "\n"))
}

func parseMarkdownMetadataKey(line string) (string, bool) {
	if !strings.HasPrefix(line, "- **") {
		return "", false
	}
	rest := strings.TrimPrefix(line, "- **")
	idx := strings.Index(rest, ":**")
	if idx < 0 {
		return "", false
	}
	return rest[:idx], true
}

func findMatchingToolResult(
	messages []conversations.StreamableMessage,
	toolUseIndex int,
	consumedResults map[int]struct{},
) (int, conversations.StreamableMessage, bool) {
	toolUse := messages[toolUseIndex]
	for i := toolUseIndex + 1; i < len(messages); i++ {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}
		candidate := messages[i]
		if candidate.Kind != "tool-result" {
			continue
		}
		if toolUse.ToolCallID != "" && candidate.ToolCallID == toolUse.ToolCallID {
			return i, candidate, true
		}
		if toolUse.ToolCallID == "" && toolUse.ToolName != "" && candidate.ToolName == toolUse.ToolName {
			return i, candidate, true
		}
	}

	return -1, conversations.StreamableMessage{}, false
}

func renderToolResultMarkdown(msg conversations.StreamableMessage, toolResults map[string]tools.StructuredToolResult, registry *renderers.RendererRegistry) string {
	var output strings.Builder
	toolName := msg.ToolName
	if toolName == "" {
		toolName = inferToolNameFromResult(msg, toolResults)
	}
	if toolName != "" {
		fmt.Fprintf(&output, "- **Tool:** %s\n", inlineMarkdownCode(toolName))
	}
	if msg.ToolCallID != "" {
		fmt.Fprintf(&output, "- **Call ID:** %s\n", inlineMarkdownCode(msg.ToolCallID))
	}

	if structuredResult, ok := lookupStructuredToolResult(msg, toolResults); ok {
		output.WriteString("\n")
		output.WriteString(registry.RenderMarkdown(structuredResult))
		return strings.TrimSpace(output.String())
	}

	trimmed := strings.TrimSpace(msg.Content)
	if trimmed != "" {
		output.WriteString("\n")
		language := "text"
		if json.Valid([]byte(trimmed)) {
			language = "json"
			trimmed = trimJSONForMarkdown(trimmed)
		}
		output.WriteString(markdownCodeFence(language, trimmed))
	}

	return strings.TrimSpace(output.String())
}

func renderToolInputMarkdown(toolName string, rawInput string) string {
	trimmed := strings.TrimSpace(rawInput)
	if trimmed == "" {
		return ""
	}

	type bashInput struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		Timeout     int    `json:"timeout"`
	}
	type fileReadInput struct {
		FilePath  string `json:"file_path"`
		Offset    int    `json:"offset"`
		LineLimit int    `json:"line_limit"`
	}
	type fileWriteInput struct {
		FilePath string `json:"file_path"`
		Text     string `json:"text"`
	}
	type fileEditInput struct {
		FilePath   string `json:"file_path"`
		OldText    string `json:"old_text"`
		NewText    string `json:"new_text"`
		ReplaceAll bool   `json:"replace_all"`
	}
	type applyPatchInput struct {
		Input string `json:"input"`
	}
	type grepInput struct {
		Pattern       string `json:"pattern"`
		Path          string `json:"path"`
		Include       string `json:"include"`
		IgnoreCase    bool   `json:"ignore_case"`
		FixedStrings  bool   `json:"fixed_strings"`
		SurroundLines int    `json:"surround_lines"`
		MaxResults    int    `json:"max_results"`
	}
	type globInput struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		IgnoreGitignore bool   `json:"ignore_gitignore"`
	}

	var output strings.Builder
	switch toolName {
	case "bash":
		var input bashInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			if input.Description != "" {
				fmt.Fprintf(&output, "- **Description:** %s\n", sanitizeMarkdownText(input.Description))
			}
			if input.Timeout > 0 {
				fmt.Fprintf(&output, "- **Timeout:** %d seconds\n", input.Timeout)
			}
			output.WriteString("\n**Command**\n\n")
			output.WriteString(markdownCodeFence("bash", input.Command))
			return strings.TrimSpace(output.String())
		}
	case "file_read":
		var input fileReadInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.FilePath))
			if input.Offset > 0 {
				fmt.Fprintf(&output, "- **Offset:** %d\n", input.Offset)
			}
			if input.LineLimit > 0 {
				fmt.Fprintf(&output, "- **Line limit:** %d\n", input.LineLimit)
			}
			return strings.TrimSpace(output.String())
		}
	case "file_write":
		var input fileWriteInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.FilePath))
			output.WriteString("\n")
			output.WriteString(markdownDetails("Requested content", markdownCodeFence("text", input.Text)))
			return strings.TrimSpace(output.String())
		}
	case "file_edit":
		var input fileEditInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.FilePath))
			if input.ReplaceAll {
				output.WriteString("- **Mode:** replace all\n")
			} else {
				output.WriteString("- **Mode:** targeted edit\n")
			}
			output.WriteString("\n")
			var request strings.Builder
			request.WriteString("**Old text**\n\n")
			request.WriteString(markdownCodeFence("text", input.OldText))
			request.WriteString("\n\n**New text**\n\n")
			request.WriteString(markdownCodeFence("text", input.NewText))
			output.WriteString(markdownDetails("Requested edit", request.String()))
			return strings.TrimSpace(output.String())
		}
	case "apply_patch":
		var input applyPatchInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			operations := summarizeApplyPatchInput(input.Input)
			if len(operations) == 0 {
				return markdownDetails("Original patch", markdownCodeFence("diff", input.Input))
			}

			fmt.Fprintf(&output, "- **Patch operations:** %d\n", len(operations))
			for _, op := range operations {
				fmt.Fprintf(&output, "- %s\n", op)
			}
			output.WriteString("\n")
			output.WriteString(markdownDetails("Original patch", markdownCodeFence("diff", input.Input)))
			return strings.TrimSpace(output.String())
		}
	case "grep_tool":
		var input grepInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineMarkdownCode(input.Pattern))
			if input.Path != "" {
				fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.Path))
			}
			if input.Include != "" {
				fmt.Fprintf(&output, "- **Include:** %s\n", inlineMarkdownCode(input.Include))
			}
			if input.SurroundLines > 0 {
				fmt.Fprintf(&output, "- **Context lines:** %d\n", input.SurroundLines)
			}
			if input.MaxResults > 0 {
				fmt.Fprintf(&output, "- **Max results:** %d\n", input.MaxResults)
			}
			if input.FixedStrings {
				output.WriteString("- **Fixed strings:** true\n")
			}
			if input.IgnoreCase {
				output.WriteString("- **Ignore case:** true\n")
			}
			return strings.TrimSpace(output.String())
		}
	case "glob_tool":
		var input globInput
		if json.Unmarshal([]byte(trimmed), &input) == nil {
			fmt.Fprintf(&output, "- **Pattern:** %s\n", inlineMarkdownCode(input.Pattern))
			if input.Path != "" {
				fmt.Fprintf(&output, "- **Path:** %s\n", inlineMarkdownCode(input.Path))
			}
			if input.IgnoreGitignore {
				output.WriteString("- **Ignore .gitignore:** true\n")
			}
			return strings.TrimSpace(output.String())
		}
	}

	return markdownCodeFence("json", trimJSONForMarkdown(trimmed))
}

func lookupStructuredToolResult(msg conversations.StreamableMessage, toolResults map[string]tools.StructuredToolResult) (tools.StructuredToolResult, bool) {
	if msg.ToolCallID != "" {
		if result, ok := toolResults[msg.ToolCallID]; ok {
			return result, true
		}
	}
	if msg.ToolName != "" {
		if result, ok := toolResults[msg.ToolName]; ok {
			return result, true
		}
	}
	return tools.StructuredToolResult{}, false
}

func inferToolNameFromResult(msg conversations.StreamableMessage, toolResults map[string]tools.StructuredToolResult) string {
	if result, ok := lookupStructuredToolResult(msg, toolResults); ok {
		return result.ToolName
	}
	return msg.ToolName
}

func markdownCodeFence(language string, content string) string {
	return renderers.FencedCodeBlock(language, content)
}

func markdownDetails(summary string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n\n</details>", summary, body)
}

func summarizeApplyPatchInput(input string) []string {
	lines := strings.Split(input, "\n")
	operations := make([]string, 0)
	currentUpdatePath := ""

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			if path != "" {
				operations = append(operations, fmt.Sprintf("Add %s", inlineMarkdownCode(path)))
			}
			currentUpdatePath = ""
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if path != "" {
				operations = append(operations, fmt.Sprintf("Delete %s", inlineMarkdownCode(path)))
			}
			currentUpdatePath = ""
		case strings.HasPrefix(line, "*** Update File: "):
			currentUpdatePath = strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			if currentUpdatePath != "" {
				operations = append(operations, fmt.Sprintf("Update %s", inlineMarkdownCode(currentUpdatePath)))
			}
		case strings.HasPrefix(line, "*** Move to: "):
			movePath := strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
			if currentUpdatePath != "" && movePath != "" && len(operations) > 0 {
				operations[len(operations)-1] = fmt.Sprintf("Update %s → %s", inlineMarkdownCode(currentUpdatePath), inlineMarkdownCode(movePath))
			}
		}
	}

	return operations
}

func inlineMarkdownCode(value string) string {
	if strings.Contains(value, "`") {
		return fmt.Sprintf("``%s``", value)
	}
	return fmt.Sprintf("`%s`", value)
}

func sanitizeMarkdownText(value string) string {
	return strings.ReplaceAll(value, "\n", " ")
}

func trimJSONForMarkdown(value string) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(value), "", "  "); err == nil {
		return pretty.String()
	}
	return value
}

func importConversationCmd(ctx context.Context, source string, config *ConversationImportConfig) {
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	data, err := readConversationData(source)
	if err != nil {
		presenter.Error(err, "Failed to read conversation data")
		os.Exit(1)
	}

	record, err := validateConversationRecord(data)
	if err != nil {
		presenter.Error(err, "Invalid conversation data")
		os.Exit(1)
	}
	if _, err := store.Load(ctx, record.ID); err == nil {
		if !config.Force {
			presenter.Error(errors.Errorf("conversation with ID %s already exists", record.ID), "Use --force to overwrite")
			os.Exit(1)
		}
	}
	if err := store.Save(ctx, *record); err != nil {
		presenter.Error(err, "Failed to save conversation")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s imported successfully", record.ID))
}

func exportConversationCmd(ctx context.Context, conversationID string, path string, config *ConversationExportConfig) {
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	record, err := store.Load(ctx, conversationID)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		os.Exit(1)
	}

	jsonData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		presenter.Error(err, "Failed to serialize conversation")
		os.Exit(1)
	}
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

	if path == "" {
		path = fmt.Sprintf("%s.json", conversationID)
	}

	if err := os.WriteFile(path, jsonData, 0o644); err != nil {
		presenter.Error(err, "Failed to write file")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s exported to %s", conversationID, path))
}

func readConversationData(source string) ([]byte, error) {
	if parsedURL, err := url.Parse(source); err == nil && parsedURL.Scheme != "" {
		return readFromURL(source)
	}

	return os.ReadFile(source)
}

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

func validateConversationRecord(data []byte) (*convtypes.ConversationRecord, error) {
	var record convtypes.ConversationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, errors.Wrap(err, "invalid JSON format")
	}

	if record.ID == "" {
		return nil, errors.New("conversation ID is required")
	}

	if record.Provider == "" {
		return nil, errors.New("model type is required")
	}

	if record.Provider != "anthropic" && record.Provider != "openai" && record.Provider != "openai-responses" && record.Provider != "google" {
		return nil, errors.Errorf("unsupported model type: %s (supported: anthropic, openai, openai-responses, google)", record.Provider)
	}

	if len(record.RawMessages) == 0 {
		return nil, errors.New("raw messages are required")
	}

	if record.ToolResults == nil {
		record.ToolResults = make(map[string]tools.StructuredToolResult)
	}

	_, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.Metadata, record.ToolResults)
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract messages")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now()
	}

	return &record, nil
}

func createGist(conversationID string, jsonData []byte, isPrivate bool) error {
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("conversation_%s_*.json", conversationID))
	if err != nil {
		return errors.Wrap(err, "failed to create temporary file")
	}
	defer os.Remove(tmpFile.Name())

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

func editConversationCmd(ctx context.Context, conversationID string, config *ConversationEditConfig) {
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	record, err := store.Load(ctx, conversationID)
	if err != nil {
		presenter.Error(err, "Failed to load conversation")
		os.Exit(1)
	}

	jsonData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		presenter.Error(err, "Failed to serialize conversation")
		os.Exit(1)
	}

	tempFile, err := os.CreateTemp("", fmt.Sprintf("conversation_%s_*.json", conversationID))
	if err != nil {
		presenter.Error(err, "Failed to create temporary file")
		os.Exit(1)
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.Write(jsonData); err != nil {
		presenter.Error(err, "Failed to write to temporary file")
		os.Exit(1)
	}
	tempFile.Close()

	editor := config.Editor
	if editor == "" {
		editor = getEditor()
	}

	editorCmd := []string{editor}
	if config.EditArgs != "" {
		args := strings.Fields(config.EditArgs)
		editorCmd = append(editorCmd, args...)
	}
	editorCmd = append(editorCmd, tempFile.Name())
	cmd := exec.Command(editorCmd[0], editorCmd[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		presenter.Error(err, "Failed to open editor")
		os.Exit(1)
	}

	editedData, err := os.ReadFile(tempFile.Name())
	if err != nil {
		presenter.Error(err, "Failed to read edited file")
		os.Exit(1)
	}

	editedRecord, err := validateConversationRecord(editedData)
	if err != nil {
		presenter.Error(err, "Invalid edited conversation data")
		os.Exit(1)
	}
	if err := store.Save(ctx, *editedRecord); err != nil {
		presenter.Error(err, "Failed to save edited conversation")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation %s edited successfully", conversationID))
}

func streamConversationCmd(ctx context.Context, conversationID string, config *ConversationStreamConfig) {
	streamer, closeFunc, err := llm.NewConversationStreamer(ctx)
	if err != nil {
		presenter.Error(err, "Failed to create conversation streamer")
		os.Exit(1)
	}
	defer closeFunc()

	service, err := conversations.GetDefaultConversationService(ctx)
	if err != nil {
		presenter.Error(err, "Failed to get conversation service")
		os.Exit(1)
	}
	defer service.Close()

	_, err = service.GetConversation(ctx, conversationID)
	if err != nil {
		presenter.Error(err, fmt.Sprintf("Conversation %s not found", conversationID))
		os.Exit(1)
	}

	liveUpdateInterval := 200 * time.Millisecond
	streamOpts := conversations.StreamOpts{
		Interval:       liveUpdateInterval,
		IncludeHistory: config.IncludeHistory,
		HistoryOnly:    config.HistoryOnly,
	}
	err = streamer.StreamLiveUpdates(ctx, conversationID, streamOpts)
	if err != nil {
		presenter.Error(err, "Failed to stream conversation updates")
		os.Exit(1)
	}
}

func forkConversationCmd(ctx context.Context, conversationID string) {
	store, err := conversations.GetConversationStore(ctx)
	if err != nil {
		presenter.Error(err, "Failed to initialize conversation store")
		os.Exit(1)
	}
	defer store.Close()

	// If no conversation ID is provided, get the most recent one
	if conversationID == "" {
		conversationID, err = conversations.GetMostRecentConversationID(ctx)
		if err != nil {
			presenter.Error(err, "Failed to get most recent conversation")
			os.Exit(1)
		}
		presenter.Info(fmt.Sprintf("Forking most recent conversation: %s", conversationID))
	}

	// Load the source conversation
	sourceRecord, err := store.Load(ctx, conversationID)
	if err != nil {
		presenter.Error(err, fmt.Sprintf("Failed to load conversation %s", conversationID))
		os.Exit(1)
	}

	// Create a new conversation record with a new ID
	forkedRecord := convtypes.NewConversationRecord("")

	// Copy messages and context from source
	forkedRecord.RawMessages = sourceRecord.RawMessages
	forkedRecord.Provider = sourceRecord.Provider
	forkedRecord.Summary = sourceRecord.Summary
	forkedRecord.ToolResults = sourceRecord.ToolResults

	// Copy FileLastAccess and Metadata maps
	if sourceRecord.FileLastAccess != nil {
		forkedRecord.FileLastAccess = make(map[string]time.Time)
		maps.Copy(forkedRecord.FileLastAccess, sourceRecord.FileLastAccess)
	}

	if sourceRecord.Metadata != nil {
		forkedRecord.Metadata = make(map[string]any)
		maps.Copy(forkedRecord.Metadata, sourceRecord.Metadata)
	}

	// Usage is already initialized to zero by NewConversationRecord
	// Preserve context window information from source
	forkedRecord.Usage.CurrentContextWindow = sourceRecord.Usage.CurrentContextWindow
	forkedRecord.Usage.MaxContextWindow = sourceRecord.Usage.MaxContextWindow

	// Save the forked conversation
	if err := store.Save(ctx, forkedRecord); err != nil {
		presenter.Error(err, "Failed to save forked conversation")
		os.Exit(1)
	}

	presenter.Success(fmt.Sprintf("Conversation forked successfully. New ID: %s", forkedRecord.ID))
	presenter.Info(fmt.Sprintf("Original: %s → Forked: %s", conversationID, forkedRecord.ID))
}
