package main

import (
	"encoding/json"
	"fmt"
	"io"
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
	Format              string
	NoHeader            bool
	StatsOnly           bool
	TruncateToolResults bool
}

func NewConversationShowConfig() *ConversationShowConfig {
	return &ConversationShowConfig{
		Format:              "text",
		NoHeader:            false,
		StatsOnly:           false,
		TruncateToolResults: false,
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
	RunE:  runRemoteConversationCommand,
}

var conversationDeleteCmd = &cobra.Command{
	Use:   "delete [conversationID]",
	Short: "Delete a specific conversation",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteConversationCommand,
}

var conversationShowCmd = &cobra.Command{
	Use:   "show [conversationID]",
	Short: "Show a specific conversation",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteConversationCommand,
}

var conversationImportCmd = &cobra.Command{
	Use:   "import [path_or_url]",
	Short: "Import a conversation from a file or URL",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteConversationCommand,
}

var conversationExportCmd = &cobra.Command{
	Use:   "export [conversationID] [path]",
	Short: "Export a conversation to a file or create a gist",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runRemoteConversationCommand,
}

var conversationEditCmd = &cobra.Command{
	Use:   "edit [conversationID]",
	Short: "Edit a conversation record in JSON format",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemoteConversationCommand,
}

var conversationForkCmd = &cobra.Command{
	Use:   "fork [conversationID]",
	Short: "Fork a conversation to create a copy with reset usage statistics",
	Long:  "Fork a conversation by copying its messages and context while resetting usage statistics (tokens and costs). If no conversation ID is provided, the most recent conversation will be forked.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runRemoteConversationCommand,
}

func init() {
	listDefaults := NewConversationListConfig()
	conversationListCmd.Flags().String("start", listDefaults.StartDate, "Filter conversations after this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("end", listDefaults.EndDate, "Filter conversations before this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().String("search", listDefaults.Search, "Search term to filter conversations")
	conversationListCmd.Flags().String("provider", listDefaults.Provider, "Filter conversations by LLM provider (anthropic, openai)")
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
	conversationShowCmd.Flags().Bool("truncate-tool-results", showDefaults.TruncateToolResults, "Truncate verbose tool results in markdown output to reduce context size")

	importDefaults := NewConversationImportConfig()
	conversationImportCmd.Flags().Bool("force", importDefaults.Force, "Force overwrite existing conversation")

	exportDefaults := NewConversationExportConfig()
	conversationExportCmd.Flags().Bool("gist", exportDefaults.UseGist, "Create a private gist using gh command")
	conversationExportCmd.Flags().Bool("public-gist", exportDefaults.UsePublicGist, "Create a public gist using gh command")

	editDefaults := NewConversationEditConfig()
	conversationEditCmd.Flags().String("editor", editDefaults.Editor, "Editor to use for editing the conversation (default: git config core.editor, then $EDITOR, then vim)")
	conversationEditCmd.Flags().String("edit-args", editDefaults.EditArgs, "Additional arguments to pass to the editor (e.g., '--wait' for VS Code)")

	conversationCmd.AddCommand(conversationListCmd)
	conversationCmd.AddCommand(conversationDeleteCmd)
	conversationCmd.AddCommand(conversationShowCmd)
	conversationCmd.AddCommand(conversationImportCmd)
	conversationCmd.AddCommand(conversationExportCmd)
	conversationCmd.AddCommand(conversationEditCmd)
	conversationCmd.AddCommand(conversationForkCmd)
	addRemoteConversationCommands(conversationCmd)
	conversationCmd.AddCommand(conversationTurnCmd)
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
	if truncateToolResults, err := cmd.Flags().GetBool("truncate-tool-results"); err == nil {
		config.TruncateToolResults = truncateToolResults
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
		platform, apiMode := conversations.ProviderMetadata(summary.Provider, metadata)

		output.Conversations = append(output.Conversations, ConversationSummaryOutput{
			ID:             summary.ID,
			CreatedAt:      summary.CreatedAt,
			UpdatedAt:      summary.UpdatedAt,
			MessageCount:   summary.MessageCount,
			Provider:       conversations.ProviderDisplayName(summary.Provider),
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

func renderConversationRecord(w io.Writer, record convtypes.ConversationRecord, config *ConversationShowConfig) error {
	platform, apiMode := conversations.ProviderMetadata(record.Provider, record.Metadata)
	providerDisplay := conversations.ProviderDisplayName(record.Provider)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	switch config.Format {
	case "raw":
		return encoder.Encode(record)
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
				return errors.Wrap(err, "failed to parse conversation messages")
			}
			output.Messages = messages
		}
		if config.NoHeader {
			return encoder.Encode(output.Messages)
		}
		return encoder.Encode(output)
	case "text":
		showHeader := !config.NoHeader
		showMessages := !config.StatsOnly
		if showHeader {
			displayConversationHeader(w, record, providerDisplay, platform, apiMode)
			if showMessages {
				fmt.Fprintln(w)
			}
		}
		if showMessages {
			messages, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.Metadata, record.ToolResults)
			if err != nil {
				return errors.Wrap(err, "failed to parse conversation messages")
			}
			displayConversation(w, messages)
		}
	case "markdown":
		showHeader := !config.NoHeader
		showMessages := !config.StatsOnly
		if showHeader {
			fmt.Fprint(w, conversations.RenderHeaderMarkdown(record))
			if showMessages {
				fmt.Fprintln(w)
			}
		}
		if showMessages {
			markdown, err := llm.RenderConversationMarkdownWithOptions(
				record.Provider,
				record.RawMessages,
				record.Metadata,
				record.ToolResults,
				llm.ConversationMarkdownOptions{
					TruncateToolResults: config.TruncateToolResults,
				},
			)
			if err != nil {
				return errors.Wrap(err, "failed to render conversation markdown")
			}
			_, err = fmt.Fprint(w, markdown)
			return err
		}
	default:
		return errors.Errorf("unsupported format %q; use raw, json, text, or markdown", config.Format)
	}
	return nil
}

func displayConversationHeader(w io.Writer, record convtypes.ConversationRecord, providerDisplay string, platform string, apiMode string) {
	presentation := presenter.NewWithOptions(w, w, presenter.ColorAuto)
	presentation.Section("Conversation Info")
	fmt.Fprintf(w, "ID:        %s\n", record.ID)
	fmt.Fprintf(w, "Provider:  %s\n", providerDisplay)
	if platform != "" {
		fmt.Fprintf(w, "Platform:  %s\n", platform)
	}
	if apiMode != "" {
		fmt.Fprintf(w, "API Mode:  %s\n", apiMode)
	}
	fmt.Fprintf(w, "Created:   %s\n", record.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Updated:   %s\n", record.UpdatedAt.Format(time.RFC3339))

	if record.Summary != "" {
		fmt.Fprintf(w, "Summary:   %s\n", record.Summary)
	}

	usage := record.Usage
	fmt.Fprintln(w)
	presentation.Section("Usage Stats")
	fmt.Fprintf(w, "Input Tokens:   %d\n", usage.InputTokens)
	fmt.Fprintf(w, "Output Tokens:  %d\n", usage.OutputTokens)
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		fmt.Fprintf(w, "Cache Read:     %d\n", usage.CacheReadInputTokens)
		fmt.Fprintf(w, "Cache Creation: %d\n", usage.CacheCreationInputTokens)
	}
	fmt.Fprintf(w, "Total Cost:     $%.4f\n", usage.TotalCost())
	if usage.MaxContextWindow > 0 {
		fmt.Fprintf(w, "Context Window: %d / %d\n", usage.CurrentContextWindow, usage.MaxContextWindow)
	}
}

func displayConversation(w io.Writer, messages []llmtypes.Message) {
	presentation := presenter.NewWithOptions(w, w, presenter.ColorAuto)
	for i, msg := range messages {
		if i > 0 {
			presentation.Separator()
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
		presentation.Section(roleLabel)
		fmt.Fprintf(w, "%s\n", msg.Content)
	}
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
