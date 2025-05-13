package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/tui"
	"github.com/spf13/cobra"
)

// ChatOptions contains all options for the chat command
type ChatOptions struct {
	usePlainUI   bool
	resumeConvID string
	storageType  string
	noSave       bool
}

var chatOptions = &ChatOptions{}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with Kodelet",
	Long:  `Start an interactive chat session with Kodelet through stdin.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Start the Bubble Tea UI
		if !chatOptions.usePlainUI {
			tui.StartChatCmd(chatOptions.resumeConvID, !chatOptions.noSave)
			return
		}

		// Use the plain CLI interface
		plainChatUI(chatOptions)
	},
}

// ListOptions contains all options for the list command
type ListOptions struct {
	startDate string
	endDate   string
	search    string
	limit     int
	offset    int
	sortBy    string
	sortOrder string
}

var listOptions = &ListOptions{}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved conversations",
	Long:  `List saved conversations with filtering and sorting options.`,
	Run: func(cmd *cobra.Command, args []string) {
		listConversationsCmd()
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete [conversationID]",
	Short: "Delete a specific conversation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		deleteConversationCmd(args[0])
	},
}

func init() {
	chatCmd.Flags().BoolVar(&chatOptions.usePlainUI, "plain", false, "Use the plain command-line interface instead of the TUI")
	chatCmd.Flags().StringVar(&chatOptions.resumeConvID, "resume", "", "Resume a specific conversation")
	chatCmd.Flags().StringVar(&chatOptions.storageType, "storage", "json", "Specify storage backend (json or sqlite)")
	chatCmd.Flags().BoolVar(&chatOptions.noSave, "no-save", false, "Disable conversation persistence")

	// Add list command flags
	listCmd.Flags().StringVar(&listOptions.startDate, "start", "", "Filter conversations after this date (format: YYYY-MM-DD)")
	listCmd.Flags().StringVar(&listOptions.endDate, "end", "", "Filter conversations before this date (format: YYYY-MM-DD)")
	listCmd.Flags().StringVar(&listOptions.search, "search", "", "Search term to filter conversations")
	listCmd.Flags().IntVar(&listOptions.limit, "limit", 0, "Maximum number of conversations to display")
	listCmd.Flags().IntVar(&listOptions.offset, "offset", 0, "Offset for pagination")
	listCmd.Flags().StringVar(&listOptions.sortBy, "sort-by", "updated", "Field to sort by: updated, created, or messages")
	listCmd.Flags().StringVar(&listOptions.sortOrder, "sort-order", "desc", "Sort order: asc (ascending) or desc (descending)")

	// Add subcommands
	chatCmd.AddCommand(listCmd)
	chatCmd.AddCommand(deleteCmd)
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

	// Create a tabwriter to format the output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tCreated\tUpdated\tMessages\tSummary\n")
	fmt.Fprintf(w, "----\t-------\t-------\t--------\t-------\n")

	for _, summary := range summaries {
		// Format creation and update dates
		created := summary.CreatedAt.Format(time.RFC3339)
		updated := summary.UpdatedAt.Format(time.RFC3339)

		// Extract first message or summary
		preview := summary.FirstMessage
		if summary.Summary != "" {
			preview = summary.Summary
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			summary.ID,
			created,
			updated,
			summary.MessageCount,
			preview,
		)
	}

	w.Flush()

	fmt.Println("\nTo resume a conversation: kodelet chat --resume <ID>")
	fmt.Println("To delete a conversation: kodelet chat delete <ID>")
}

// deleteConversationCmd deletes a specific conversation
func deleteConversationCmd(id string) {
	// Create a store
	store, err := conversations.GetConversationStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Confirm deletion
	fmt.Printf("Are you sure you want to delete conversation %s? (y/N): ", id)
	var response string
	fmt.Scanln(&response)

	if response != "y" && response != "Y" {
		fmt.Println("Deletion cancelled.")
		return
	}

	// Delete the conversation
	err = store.Delete(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting conversation: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Conversation %s deleted successfully.\n", id)
}
