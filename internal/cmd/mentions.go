package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
)

var mentionsCmd = &cobra.Command{
	Use:   "mentions",
	Short: "Show messages where you were @mentioned",
	RunE:  runMentions,
}

var (
	mentionsChannel string
	mentionsLimit   int
	mentionsUnread  bool
	mentionsSince   string
)

func init() {
	mentionsCmd.Flags().StringVar(&mentionsChannel, "channel", "", "scope to channel")
	mentionsCmd.Flags().IntVar(&mentionsLimit, "limit", 50, "max results")
	mentionsCmd.Flags().BoolVar(&mentionsUnread, "unread", false, "only unread mentions")
	mentionsCmd.Flags().StringVar(&mentionsSince, "since", "", "mentions since date (YYYY-MM-DD)")
}

func runMentions(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() { _ = store.Close() }()

	opts := db.MentionOptions{
		Channel: mentionsChannel,
		Limit:   mentionsLimit,
		Unread:  mentionsUnread,
		Since:   mentionsSince,
	}

	messages, err := store.GetMentions(opts)
	if err != nil {
		return fmt.Errorf("get mentions: %w", err)
	}

	output.Print(output.MessageListResult{Messages: messages})
	return nil
}
