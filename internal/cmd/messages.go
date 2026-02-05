package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
	"slacli/internal/sync"
)

const (
	// Auto-sync if last sync was more than this long ago
	// Since sync now only fetches channels with actual changes (unread or activity),
	// we can run it more frequently without API overhead
	autoSyncStaleThreshold = 5 * time.Minute
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

var messagesUnreadCmd = &cobra.Command{
	Use:   "unread",
	Short: "List unread messages across channels",
	RunE:  runMessagesUnread,
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

	// Unread flags
	msgUnreadLimit   int
	msgUnreadRefresh bool
)

func init() {
	messagesCmd.AddCommand(messagesListCmd)
	messagesCmd.AddCommand(messagesSearchCmd)
	messagesCmd.AddCommand(messagesContextCmd)
	messagesCmd.AddCommand(messagesUnreadCmd)

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

	// Auto-sync if stale
	if err := autoSyncIfStale(cfg); err != nil {
		output.Debug("Auto-sync failed: %v", err)
		// Continue with search even if sync fails
	}

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

// autoSyncIfStale runs a quick sync if the last sync was too long ago
func autoSyncIfStale(cfg *config.Config) error {
	state, err := db.LoadSyncState(cfg)
	if err != nil {
		// No sync state = never synced, but don't auto-sync on first run
		// User should run `slack sync` explicitly first
		return nil
	}

	if state.LastSync == "" {
		return nil
	}

	lastSync, err := time.Parse(time.RFC3339, state.LastSync)
	if err != nil {
		return nil
	}

	if time.Since(lastSync) < autoSyncStaleThreshold {
		// Recently synced, skip
		return nil
	}

	// Stale, run quick sync
	output.Info("Auto-syncing (last sync: %s ago)...", time.Since(lastSync).Round(time.Second))

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	syncer := sync.New(cfg, client)
	result, err := syncer.Run(sync.Options{
		MyChannels: true,
		Days:       cfg.SyncDays,
	})
	if err != nil {
		return err
	}

	if result.MessagesSynced > 0 {
		output.Info("Synced %d new messages", result.MessagesSynced)
	}

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
	defer store.Close()

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
