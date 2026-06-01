package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
	"slacli/internal/slack"
	"slacli/internal/sync"
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
	Short: "Search messages using local index and Slack API",
	Args:  cobra.ExactArgs(1),
	RunE:  runMessagesSearch,
}

var messagesContextCmd = &cobra.Command{
	Use:   "context <message_id>",
	Short: "Show messages around a specific message",
	Args:  cobra.ExactArgs(1),
	RunE:  runMessagesContext,
}

var messagesUnreadCmd = &cobra.Command{
	Use:   "unread",
	Short: "List unread messages across channels",
	RunE:  runMessagesUnread,
}

var messagesEditCmd = &cobra.Command{
	Use:   "edit <channel> <timestamp> <new-text>",
	Short: "Edit an existing message",
	Long: `Edit a message you previously sent.

You can only edit your own messages. The message will show an "edited" indicator.

Examples:
  slack messages edit "#general" 1704540600.123456 "Updated message text"
  slack messages edit C123ABC 1704540600.123456 "Fixed typo"`,
	Args: cobra.ExactArgs(3),
	RunE: runMessagesEdit,
}

var messagesDeleteCmd = &cobra.Command{
	Use:   "delete <channel> <timestamp>",
	Short: "Delete a message",
	Long: `Delete a message you previously sent.

You can only delete your own messages (unless you're a workspace admin).

Examples:
  slack messages delete "#general" 1704540600.123456
  slack messages delete C123ABC 1704540600.123456`,
	Args: cobra.ExactArgs(2),
	RunE: runMessagesDelete,
}

var messagesPermalinkCmd = &cobra.Command{
	Use:   "permalink <channel> <timestamp>",
	Short: "Get a permalink URL for a message",
	Long: `Get the permanent URL for a specific message.

Examples:
  slack messages permalink "#general" 1704540600.123456
  slack messages permalink C123ABC 1704540600.123456`,
	Args: cobra.ExactArgs(2),
	RunE: runMessagesPermalink,
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
	msgSearchLocal   bool
	msgSearchLive    bool

	// Context flags
	msgContextBefore int
	msgContextAfter  int

	// Unread flags
	msgUnreadLimit   int
	msgUnreadRefresh bool
)

func init() {
	messagesCmd.AddCommand(messagesListCmd)
	messagesCmd.AddCommand(messagesSearchCmd)
	messagesCmd.AddCommand(messagesContextCmd)
	messagesCmd.AddCommand(messagesUnreadCmd)
	messagesCmd.AddCommand(messagesEditCmd)
	messagesCmd.AddCommand(messagesDeleteCmd)
	messagesCmd.AddCommand(messagesPermalinkCmd)

	// List flags
	messagesListCmd.Flags().StringVar(&msgListChannel, "channel", "", "channel name, ID, or email for DM (required)")
	messagesListCmd.Flags().IntVar(&msgListLimit, "limit", 50, "max messages")
	messagesListCmd.Flags().StringVar(&msgListBefore, "before", "", "messages before timestamp")
	messagesListCmd.Flags().StringVar(&msgListAfter, "after", "", "messages after timestamp")
	messagesListCmd.Flags().StringVar(&msgListThread, "thread", "", "show thread replies for message")
	cobra.CheckErr(messagesListCmd.MarkFlagRequired("channel"))

	// Search flags
	messagesSearchCmd.Flags().StringVar(&msgSearchChannel, "channel", "", "scope to channel/DM")
	messagesSearchCmd.Flags().StringVar(&msgSearchFrom, "from", "", "filter by author (email)")
	messagesSearchCmd.Flags().StringVar(&msgSearchAfter, "after", "", "messages after date (YYYY-MM-DD)")
	messagesSearchCmd.Flags().StringVar(&msgSearchBefore, "before", "", "messages before date")
	messagesSearchCmd.Flags().IntVar(&msgSearchLimit, "limit", 50, "max results")
	messagesSearchCmd.Flags().BoolVar(&msgSearchLocal, "local", false, "search local index only")
	messagesSearchCmd.Flags().BoolVar(&msgSearchLive, "live", false, "search Slack API only")

	// Context flags
	messagesContextCmd.Flags().IntVar(&msgContextBefore, "before", 5, "messages before")
	messagesContextCmd.Flags().IntVar(&msgContextAfter, "after", 5, "messages after")

	// Unread flags
	messagesUnreadCmd.Flags().IntVar(&msgUnreadLimit, "limit", 50, "max messages")
	messagesUnreadCmd.Flags().BoolVar(&msgUnreadRefresh, "refresh", false, "sync unread channels first")
}

func runMessagesList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() { _ = store.Close() }()

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
	return runSearch(args[0], searchCLIOptions{
		Channel:   msgSearchChannel,
		From:      msgSearchFrom,
		After:     msgSearchAfter,
		Before:    msgSearchBefore,
		Limit:     msgSearchLimit,
		LocalOnly: msgSearchLocal,
		LiveOnly:  msgSearchLive,
	})
}

func runMessagesContext(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	messageID := args[0]

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() { _ = store.Close() }()

	messages, err := store.GetMessageContext(messageID, msgContextBefore, msgContextAfter)
	if err != nil {
		return fmt.Errorf("get context: %w", err)
	}

	output.Print(output.MessageListResult{Messages: messages})
	return nil
}

func runMessagesUnread(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	// Optionally refresh unread status from API
	if msgUnreadRefresh {
		output.Info("Refreshing unread status...")
		if err := syncUnreadChannels(cfg); err != nil {
			output.Debug("Refresh failed: %v", err)
		}
	}

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() { _ = store.Close() }()

	messages, err := store.GetUnreadMessages(msgUnreadLimit)
	if err != nil {
		return fmt.Errorf("get unread: %w", err)
	}

	output.Print(output.MessageListResult{Messages: messages})
	return nil
}

// syncUnreadChannels fetches unread status from API and syncs those channels
func syncUnreadChannels(cfg *config.Config) error {
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	syncer := sync.New(cfg, client)
	result, err := syncer.Run(sync.Options{
		UnreadOnly: true,
		Days:       cfg.SyncDays,
	})
	if err != nil {
		return err
	}

	if result.MessagesSynced > 0 {
		output.Info("Synced %d new messages from %d unread channels", result.MessagesSynced, result.ChannelsSynced)
	}

	return nil
}

func runMessagesEdit(cmd *cobra.Command, args []string) error {
	channel := args[0]
	timestamp := args[1]
	newText := args[2]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	// Resolve channel
	channelID, err := api.ResolveChannel(channel)
	if err != nil {
		return fmt.Errorf("resolve channel: %w", err)
	}

	result, err := api.UpdateMessage(channelID, timestamp, newText)
	if err != nil {
		return err
	}

	output.Success(fmt.Sprintf("Updated message %s", result.Timestamp))
	output.Print(result)
	return nil
}

func runMessagesDelete(cmd *cobra.Command, args []string) error {
	channel := args[0]
	timestamp := args[1]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	// Resolve channel
	channelID, err := api.ResolveChannel(channel)
	if err != nil {
		return fmt.Errorf("resolve channel: %w", err)
	}

	if err := api.DeleteMessage(channelID, timestamp); err != nil {
		return err
	}

	output.Success(fmt.Sprintf("Deleted message %s", timestamp))
	return nil
}

func runMessagesPermalink(cmd *cobra.Command, args []string) error {
	channel := args[0]
	timestamp := args[1]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	// Resolve channel
	channelID, err := api.ResolveChannel(channel)
	if err != nil {
		return fmt.Errorf("resolve channel: %w", err)
	}

	permalink, err := api.GetPermalink(channelID, timestamp)
	if err != nil {
		return err
	}

	output.Print(map[string]string{"permalink": permalink})
	return nil
}
