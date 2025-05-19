package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/spf13/cobra"
)

// ConversationOptions contains common options for conversation commands
type ConversationOptions struct {
	// Common options can be added here
}

var conversationOptions = &ConversationOptions{}

var conversationCmd = &cobra.Command{
	Use:   "conversation",
	Short: "Manage saved conversations",
	Long:  `List, view, and delete saved conversations.`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

// ListOptions contains all options for the list command
type ListOptions struct {
	startDate  string
	endDate    string
	search     string
	limit      int
	offset     int
	sortBy     string
	sortOrder  string
	jsonOutput bool
}

var listOptions = &ListOptions{}

var conversationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved conversations",
	Long:  `List saved conversations with filtering and sorting options.`,
	Run: func(cmd *cobra.Command, args []string) {
		listConversationsCmd()
	},
}

// DeleteOptions contains options for the delete command
type DeleteOptions struct {
	noConfirm bool
}

var deleteOptions = &DeleteOptions{}

var conversationDeleteCmd = &cobra.Command{
	Use:   "delete [conversationID]",
	Short: "Delete a specific conversation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		deleteConversationCmd(args[0])
	},
}

// ShowOptions contains options for the show command
type ShowOptions struct {
	format string
}

var showOptions = &ShowOptions{}

var conversationShowCmd = &cobra.Command{
	Use:   "show [conversationID]",
	Short: "Show a specific conversation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		showConversationCmd(args[0])
	},
}

func init() {
	// Add list command flags
	conversationListCmd.Flags().StringVar(&listOptions.startDate, "start", "", "Filter conversations after this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().StringVar(&listOptions.endDate, "end", "", "Filter conversations before this date (format: YYYY-MM-DD)")
	conversationListCmd.Flags().StringVar(&listOptions.search, "search", "", "Search term to filter conversations")
	conversationListCmd.Flags().IntVar(&listOptions.limit, "limit", 0, "Maximum number of conversations to display")
	conversationListCmd.Flags().IntVar(&listOptions.offset, "offset", 0, "Offset for pagination")
	conversationListCmd.Flags().StringVar(&listOptions.sortBy, "sort-by", "updated", "Field to sort by: updated, created, or messages")
	conversationListCmd.Flags().StringVar(&listOptions.sortOrder, "sort-order", "desc", "Sort order: asc (ascending) or desc (descending)")
	conversationListCmd.Flags().BoolVar(&listOptions.jsonOutput, "json", false, "Output in JSON format")

	// Add delete command flags
	conversationDeleteCmd.Flags().BoolVar(&deleteOptions.noConfirm, "no-confirm", false, "Skip confirmation prompt")

	// Add show command flags
	conversationShowCmd.Flags().StringVar(&showOptions.format, "format", "text", "Output format: raw, json, or text")

	// Add subcommands
	conversationCmd.AddCommand(conversationListCmd)
	conversationCmd.AddCommand(conversationDeleteCmd)
	conversationCmd.AddCommand(conversationShowCmd)
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
func listConversationsCmd() {
	// Create a store
	store, err := conversations.GetConversationStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Prepare query options
	options := conversations.QueryOptions{
		SearchTerm: listOptions.search,
		Limit:      listOptions.limit,
		Offset:     listOptions.offset,
		SortBy:     listOptions.sortBy,
		SortOrder:  listOptions.sortOrder,
	}

	// Parse start date if provided
	if listOptions.startDate != "" {
		startDate, err := time.Parse("2006-01-02", listOptions.startDate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing start date: %v\n", err)
			os.Exit(1)
		}
		options.StartDate = &startDate
	}

	// Parse end date if provided
	if listOptions.endDate != "" {
		endDate, err := time.Parse("2006-01-02", listOptions.endDate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing end date: %v\n", err)
			os.Exit(1)
		}
		// Set to end of day
		endDate = endDate.Add(24*time.Hour - time.Second)
		options.EndDate = &endDate
	}

	// Query conversations with options
	summaries, err := store.Query(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing conversations: %v\n", err)
		os.Exit(1)
	}

	if len(summaries) == 0 {
		fmt.Println("No conversations found matching your criteria.")
		return
	}

	// Determine output format
	format := TableFormat
	if listOptions.jsonOutput {
		format = JSONFormat
	}

	// Create and render the output
	output := NewConversationListOutput(summaries, format)
	if err := output.Render(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error rendering output: %v\n", err)
		os.Exit(1)
	}
}

// deleteConversationCmd deletes a specific conversation
func deleteConversationCmd(id string) {
	// Create a store
	store, err := conversations.GetConversationStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// If no-confirm flag is not set, prompt for confirmation
	if !deleteOptions.noConfirm {
		fmt.Printf("Are you sure you want to delete conversation %s? (y/N): ", id)
		var response string
		fmt.Scanln(&response)

		if response != "y" && response != "Y" {
			fmt.Println("Deletion cancelled.")
			return
		}
	}

	// Delete the conversation
	err = store.Delete(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting conversation: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Conversation %s deleted successfully.\n", id)
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// showConversationCmd displays a specific conversation
func showConversationCmd(id string) {
	// Create a store
	store, err := conversations.GetConversationStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Load the conversation record
	record, err := store.Load(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading conversation: %v\n", err)
		os.Exit(1)
	}

	// Extract messages from raw message data
	messages, err := extractMessages(record.RawMessages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing messages: %v\n", err)
		os.Exit(1)
	}

	// Render messages according to the format
	switch showOptions.format {
	case "raw":
		// Output the raw messages as stored
		fmt.Println(string(record.RawMessages))
	case "json":
		// Convert to simpler JSON format and output
		outputJSON, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating JSON output: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(outputJSON))
	case "text":
		// Format as readable text with user/assistant prefixes
		displayConversation(messages)
	default:
		fmt.Fprintf(os.Stderr, "Unknown format: %s. Supported formats are raw, json, and text.\n", showOptions.format)
		os.Exit(1)
	}
}

// extractMessages parses the raw messages from a conversation record
func extractMessages(rawMessages json.RawMessage) ([]Message, error) {
	// Parse the raw JSON messages directly
	var rawMsgs []map[string]interface{}
	if err := json.Unmarshal(rawMessages, &rawMsgs); err != nil {
		return nil, fmt.Errorf("error parsing raw messages: %v", err)
	}

	var messages []Message
	for _, msg := range rawMsgs {
		role, ok := msg["role"].(string)
		if !ok {
			continue // Skip if role is not a string or doesn't exist
		}

		content, ok := msg["content"].([]interface{})
		if !ok || len(content) == 0 {
			continue // Skip if content is not an array or is empty
		}

		// Process each content block in the message
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				continue // Skip if block is not a map
			}

			// Extract block type
			blockType, ok := blockMap["type"].(string)
			if !ok {
				continue // Skip if type is not a string or doesn't exist
			}

			// Extract message content based on block type
			switch blockType {
			case "text":
				// Add text content
				text, ok := blockMap["text"].(string)
				if !ok {
					continue // Skip if text is not a string or doesn't exist
				}

				messages = append(messages, Message{
					Role:    role,
					Content: text,
				})

			case "tool_use":
				// Add tool usage as content
				input, ok := blockMap["input"]
				if !ok {
					continue // Skip if input is not found
				}

				inputJSON, err := json.Marshal(input)
				if err != nil {
					continue // Skip if marshaling fails
				}

				messages = append(messages, Message{
					Role:    role,
					Content: fmt.Sprintf("🔧 Using tool: %s", string(inputJSON)),
				})

			case "tool_result":
				// Add tool result as content
				resultContent, ok := blockMap["content"].([]interface{})
				if !ok || len(resultContent) == 0 {
					continue // Skip if content is not an array or is empty
				}

				resultBlock, ok := resultContent[0].(map[string]interface{})
				if !ok {
					continue // Skip if first element is not a map
				}

				if resultBlock["type"] == "text" {
					result, ok := resultBlock["text"].(string)
					if !ok {
						continue // Skip if text is not a string
					}

					messages = append(messages, Message{
						Role:    "assistant",
						Content: fmt.Sprintf("🔄 Tool result: %s", result),
					})
				}

			case "thinking":
				// Add thinking content
				thinking, ok := blockMap["thinking"].(string)
				if !ok {
					continue // Skip if thinking is not a string
				}

				messages = append(messages, Message{
					Role:    "assistant",
					Content: fmt.Sprintf("💭 Thinking: %s", thinking),
				})
			}
		}
	}

	return messages, nil
}

// displayConversation renders the messages in a readable text format
func displayConversation(messages []Message) {
	for i, msg := range messages {
		// Add a separator between messages
		if i > 0 {
			fmt.Println(strings.Repeat("-", 80))
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

		// Output the formatted message
		fmt.Printf("%s:\n%s\n", roleLabel, msg.Content)
	}
}
