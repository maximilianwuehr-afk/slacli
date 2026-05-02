package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
)

var channelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "Manage channels and DMs",
}

var channelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List channels and DMs",
	RunE:  runChannelsList,
}

var (
	channelsSortBy string
	channelsType   string
	channelsLimit  int
	channelsUnread bool
)

func init() {
	channelsCmd.AddCommand(channelsListCmd)

	channelsListCmd.Flags().StringVar(&channelsSortBy, "sort", "last_received", "sort by: last_sent|last_received|last_mention|name")
	channelsListCmd.Flags().StringVar(&channelsType, "type", "all", "filter: all|channel|dm|group_dm|private")
	channelsListCmd.Flags().IntVar(&channelsLimit, "limit", 50, "max results")
	channelsListCmd.Flags().BoolVar(&channelsUnread, "unread", false, "only show channels with unread messages")
}

func runChannelsList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() { _ = store.Close() }()

	opts := db.ChannelListOptions{
		SortBy: channelsSortBy,
		Type:   channelsType,
		Limit:  channelsLimit,
		Unread: channelsUnread,
	}

	channels, err := store.ListChannels(opts)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	output.Print(output.ChannelListResult{Channels: channels})
	return nil
}
