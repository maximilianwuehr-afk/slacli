package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

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
	Use:   "context <message_id> | <channel> <timestamp>",
	Short: "Show messages around a message ID or channel timestamp",
	Long: `Show messages around a Slack message.

Context reads the local cache. Pass the Slack timestamp as <message_id>,
or pass the channel and timestamp. Run slack sync to populate the cache.

Examples:
  slack messages context 1704540600.123456
  slack messages context "#general" 1704540600.123456`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runMessagesContext,
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

	messages, err := listMessagesWithThreadFallback(store, opts, func() (*slack.API, error) {
		client, err := auth.GetClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("auth required: %w", err)
		}
		return slack.NewAPI(client), nil
	})
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
	channel, messageID, err := parseMessageContextArgs(args)
	if err != nil {
		return err
	}

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() { _ = store.Close() }()

	messages, err := store.GetMessageContext(messageID, msgContextBefore, msgContextAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("message %s is not in the local cache; context requires a synced message (run: slack sync)", messageID)
	}
	if err != nil {
		return fmt.Errorf("get context: %w", err)
	}
	if channel != "" && !contextContainsChannelMessage(messages, channel, messageID) {
		return fmt.Errorf("message %s not found in channel %s", messageID, channel)
	}

	output.Print(output.MessageListResult{Messages: messages})
	return nil
}

func parseMessageContextArgs(args []string) (channel, messageID string, err error) {
	switch len(args) {
	case 1:
		return "", args[0], nil
	case 2:
		return args[0], args[1], nil
	default:
		return "", "", fmt.Errorf("expected <message_id> or <channel> <timestamp>")
	}
}

func contextContainsChannelMessage(messages []output.Message, channel, messageID string) bool {
	channel = strings.TrimPrefix(strings.TrimPrefix(channel, "#"), "@")
	for _, message := range messages {
		if message.ID != messageID {
			continue
		}
		if strings.EqualFold(message.ChannelID, channel) || strings.EqualFold(message.ChannelName, channel) {
			return true
		}
	}
	return false
}

func listMessagesWithThreadFallback(
	store *db.Store,
	opts db.MessageListOptions,
	newAPI func() (*slack.API, error),
) ([]output.Message, error) {
	var err error
	opts.Before, err = messageFilterTimestamp(opts.Before)
	if err != nil {
		return nil, fmt.Errorf("invalid --before: %w", err)
	}
	opts.After, err = messageFilterTimestamp(opts.After)
	if err != nil {
		return nil, fmt.Errorf("invalid --after: %w", err)
	}
	messages, err := store.ListMessages(opts)
	if err != nil {
		// A channel name that is not in the local channel cache can still be
		// resolved by Slack when the requested thread needs a live fetch.
		if opts.ThreadTS == "" || !strings.HasPrefix(err.Error(), "channel not found: ") {
			return nil, err
		}
		messages = []output.Message{}
	}
	if messages == nil {
		messages = []output.Message{}
	}
	if opts.ThreadTS == "" {
		return messages, nil
	}

	cached := messages
	if len(cached) == 0 {
		unfiltered := opts
		unfiltered.Before, unfiltered.After, unfiltered.Limit = "", "", 1
		cached, err = store.ListMessages(unfiltered)
		if err != nil && !strings.HasPrefix(err.Error(), "channel not found: ") {
			return nil, err
		}
	}
	channelID := ""
	if len(cached) > 0 {
		channelID = cached[0].ChannelID
		cacheMiss, err := threadCacheMiss(store, channelID, opts.ThreadTS)
		if err != nil {
			return nil, fmt.Errorf("check thread cache: %w", err)
		}
		if !cacheMiss {
			return messages, nil
		}
	}

	api, err := newAPI()
	if err != nil {
		return nil, fmt.Errorf("create Slack client: %w", err)
	}
	if channelID == "" {
		channelID, err = api.ResolveChannel(opts.Channel)
		if err != nil {
			return nil, fmt.Errorf("resolve channel: %w", err)
		}
	}

	messages, err = fetchLiveThreadMessages(api, channelID, opts)
	if err != nil {
		return nil, fmt.Errorf("fetch thread replies: %w", err)
	}
	return messages, nil
}

// Compare Slack IDs, since older cached display timestamps lost fractional seconds.
func messageFilterTimestamp(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return fmt.Sprintf("%d.%09d", parsed.Unix(), parsed.Nanosecond()), nil
		}
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return "", fmt.Errorf("expected a Slack timestamp, RFC3339 timestamp, or YYYY-MM-DD")
	}
	return value, nil
}

func threadCacheMiss(store *db.Store, channelID, threadTS string) (bool, error) {
	var replyCount int
	err := store.DB().QueryRow(
		"SELECT reply_count FROM messages WHERE channel_id = ? AND id = ?",
		channelID, threadTS,
	).Scan(&replyCount)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	var cachedReplies int
	if err := store.DB().QueryRow(
		"SELECT COUNT(*) FROM messages WHERE channel_id = ? AND thread_ts = ? AND id != ?",
		channelID, threadTS, threadTS,
	).Scan(&cachedReplies); err != nil {
		return false, err
	}
	return cachedReplies < replyCount, nil
}

func fetchLiveThreadMessages(api *slack.API, channelID string, opts db.MessageListOptions) ([]output.Message, error) {
	messages := []output.Message{}
	before, _ := strconv.ParseFloat(opts.Before, 64)
	after, _ := strconv.ParseFloat(opts.After, 64)
	cursor := ""
	seenCursors := map[string]struct{}{}
	for {
		resp, err := api.GetReplies(channelID, opts.ThreadTS, cursor, 200)
		if err != nil {
			return nil, err
		}

		for _, message := range resp.Messages {
			timestamp := slack.TimestampToRFC3339(message.TS)
			seconds, err := strconv.ParseFloat(message.TS, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid message timestamp: %w", err)
			}
			if (opts.Before != "" && seconds >= before) || (opts.After != "" && seconds <= after) {
				continue
			}
			converted := output.Message{
				ID:         message.TS,
				ChannelID:  channelID,
				AuthorID:   message.User,
				AuthorName: message.User,
				Text:       message.Text,
				Timestamp:  timestamp,
				ThreadTS:   message.ThreadTS,
				ReplyCount: message.ReplyCount,
				Edited:     message.Edited != nil,
			}
			for _, reaction := range message.Reactions {
				converted.Reactions = append(converted.Reactions, output.Reaction{
					Name:  reaction.Name,
					Count: reaction.Count,
				})
			}
			messages = append(messages, converted)
		}

		nextCursor := resp.ResponseMetadata.NextCursor
		if nextCursor == "" {
			break
		}
		if _, seen := seenCursors[nextCursor]; seen {
			return nil, fmt.Errorf("conversations.replies returned repeated cursor %q", nextCursor)
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}

	sort.Slice(messages, func(i, j int) bool { return messages[i].ID > messages[j].ID })
	if opts.Limit > 0 && len(messages) > opts.Limit {
		messages = messages[:opts.Limit]
	}
	return messages, nil
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
