package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
)

var messagesCmd = &cobra.Command{
	Use:   "messages",
	Short: "Read and search messages",
}

var messagesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List messages from a channel or DM",
	RunE:  runMessagesList,
}

var messagesSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search messages using full-text search",
	Args:  cobra.ExactArgs(1),
	RunE:  runMessagesSearch,
}

var messagesContextCmd = &cobra.Command{
	Use:   "context <message_id>",
	Short: "Show messages around a specific message",
	Args:  cobra.ExactArgs(1),
	RunE:  runMessagesContext,
}

var (
	// List flags
	msgListChannel string
	msgListLimit   int
	msgListBefore  string
	msgListAfter   string
	msgListThread  string

	// Search flags
	msgSearchChannel string
	msgSearchFrom    string
	msgSearchAfter   string
	msgSearchBefore  string
	msgSearchLimit   int

	// Context flags
	msgContextBefore int
	msgContextAfter  int
)

func init() {
	messagesCmd.AddCommand(messagesListCmd)
	messagesCmd.AddCommand(messagesSearchCmd)
	messagesCmd.AddCommand(messagesContextCmd)

	// List flags
	messagesListCmd.Flags().StringVar(&msgListChannel, "channel", "", "channel name, ID, or email for DM (required)")
	messagesListCmd.Flags().IntVar(&msgListLimit, "limit", 50, "max messages")
	messagesListCmd.Flags().StringVar(&msgListBefore, "before", "", "messages before timestamp")
	messagesListCmd.Flags().StringVar(&msgListAfter, "after", "", "messages after timestamp")
	messagesListCmd.Flags().StringVar(&msgListThread, "thread", "", "show thread replies for message")
	messagesListCmd.MarkFlagRequired("channel")

	// Search flags
	messagesSearchCmd.Flags().StringVar(&msgSearchChannel, "channel", "", "scope to channel/DM")
	messagesSearchCmd.Flags().StringVar(&msgSearchFrom, "from", "", "filter by author (email)")
	messagesSearchCmd.Flags().StringVar(&msgSearchAfter, "after", "", "messages after date (YYYY-MM-DD)")
	messagesSearchCmd.Flags().StringVar(&msgSearchBefore, "before", "", "messages before date")
	messagesSearchCmd.Flags().IntVar(&msgSearchLimit, "limit", 50, "max results")

	// Context flags
	messagesContextCmd.Flags().IntVar(&msgContextBefore, "before", 5, "messages before")
	messagesContextCmd.Flags().IntVar(&msgContextAfter, "after", 5, "messages after")
}

func runMessagesList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer store.Close()

	opts := db.MessageListOptions{
		Channel:  msgListChannel,
		Limit:    msgListLimit,
		Before:   msgListBefore,
		After:    msgListAfter,
		ThreadTS: msgListThread,
	}

	messages, err := store.ListMessages(opts)
	if err != nil {
		return fmt.Errorf("list messages: %w", err)
	}

	output.Print(output.MessageListResult{Messages: messages})
	return nil
}

func runMessagesSearch(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	query := args[0]

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer store.Close()

	opts := db.SearchOptions{
		Query:   query,
		Channel: msgSearchChannel,
		From:    msgSearchFrom,
		After:   msgSearchAfter,
		Before:  msgSearchBefore,
		Limit:   msgSearchLimit,
	}

	messages, err := store.SearchMessages(opts)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	output.Print(output.MessageListResult{Messages: messages})
	return nil
}

func runMessagesContext(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	messageID := args[0]

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer store.Close()

	messages, err := store.GetMessageContext(messageID, msgContextBefore, msgContextAfter)
	if err != nil {
		return fmt.Errorf("get context: %w", err)
	}

	output.Print(output.MessageListResult{Messages: messages})
	return nil
}
