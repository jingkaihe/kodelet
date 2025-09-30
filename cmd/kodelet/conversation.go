package main

import (
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

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
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
	Format string
}

func NewConversationShowConfig() *ConversationShowConfig {
	return &ConversationShowConfig{
		Format: "text",
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

type ConversationStreamConfig struct {
	IncludeHistory bool
}

func NewConversationStreamConfig() *ConversationStreamConfig {
	return &ConversationStreamConfig{
		IncludeHistory: false,
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
	conversationListCmd.Flags().String("provider", listDefaults.Provider, "Filter conversations by LLM provider (anthropic or openai)")
	conversationListCmd.Flags().Int("limit", listDefaults.Limit, "Maximum number of conversations to display")
	conversationListCmd.Flags().Int("offset", listDefaults.Offset, "Offset for pagination")
	conversationListCmd.Flags().String("sort-by", listDefaults.SortBy, "Field to sort by: updated_at, created_at, or messages")
	conversationListCmd.Flags().String("sort-order", listDefaults.SortOrder, "Sort order: asc (ascending) or desc (descending)")
	conversationListCmd.Flags().Bool("json", listDefaults.JSONOutput, "Output in JSON format")

	deleteDefaults := NewConversationDeleteConfig()
	conversationDeleteCmd.Flags().Bool("no-confirm", deleteDefaults.NoConfirm, "Skip confirmation prompt")

	showDefaults := NewConversationShowConfig()
	conversationShowCmd.Flags().String("format", showDefaults.Format, "Output format: raw, json, or text")

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

func NewConversationListOutput(summaries []convtypes.ConversationSummary, format OutputFormat) *ConversationListOutput {
	output := &ConversationListOutput{
		Conversations: make([]ConversationSummaryOutput, 0, len(summaries)),
		Format:        format,
	}

	for _, summary := range summaries {
		preview := summary.FirstMessage
		if summary.Summary != "" {
			preview = summary.Summary
		}

		// Remove newlines from preview to keep table formatting clean
		preview = strings.ReplaceAll(preview, "\n", " ")
		preview = strings.ReplaceAll(preview, "\r", " ")

		provider := summary.Provider
		switch summary.Provider {
		case "anthropic":
			provider = "Anthropic"
		case "openai":
			provider = "OpenAI"
		case "google":
			provider = "Google"
		}

		output.Conversations = append(output.Conversations, ConversationSummaryOutput{
			ID:             summary.ID,
			CreatedAt:      summary.CreatedAt,
			UpdatedAt:      summary.UpdatedAt,
			MessageCount:   summary.MessageCount,
			Provider:       provider,
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

	fmt.Fprintln(tw, "ID\tCreated\tUpdated\tMessages\tProvider\tCost\tContext\tSummary")
	fmt.Fprintln(tw, "----\t-------\t-------\t--------\t--------\t----\t-------\t-------")

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

		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			summary.ID,
			created,
			updated,
			summary.MessageCount,
			summary.Provider,
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

	format := TableFormat
	if config.JSONOutput {
		format = JSONFormat
	}
	output := NewConversationListOutput(summaries, format)
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

	presenter.Success(fmt.Sprintf("Conversation %s deleted successfully", id))
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

	messages, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.ToolResults)
	if err != nil {
		presenter.Error(err, "Failed to parse conversation messages")
		os.Exit(1)
	}
	switch config.Format {
	case "raw":
		fmt.Println(string(record.RawMessages))
	case "json":
		outputJSON, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			presenter.Error(err, "Failed to generate JSON output")
			os.Exit(1)
		}
		fmt.Println(string(outputJSON))
	case "text":
		displayConversation(messages)
	default:
		presenter.Error(errors.Errorf("unsupported format: %s", config.Format), "Unknown format. Supported formats are raw, json, and text")
		os.Exit(1)
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

	if err := os.WriteFile(path, jsonData, 0644); err != nil {
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

	if record.Provider != "anthropic" && record.Provider != "openai" {
		return nil, errors.Errorf("unsupported model type: %s (supported: anthropic, openai)", record.Provider)
	}

	if len(record.RawMessages) == 0 {
		return nil, errors.New("raw messages are required")
	}

	if record.ToolResults == nil {
		record.ToolResults = make(map[string]tools.StructuredToolResult)
	}

	_, err := llm.ExtractMessages(record.Provider, record.RawMessages, record.ToolResults)
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
	forkedRecord.BackgroundProcesses = sourceRecord.BackgroundProcesses

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
