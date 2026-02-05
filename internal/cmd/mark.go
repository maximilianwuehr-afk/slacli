package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var (
	markTimestamp string
)

var markCmd = &cobra.Command{
	Use:   "mark <channel>",
	Short: "Mark a channel as read",
	Long: `Mark a channel as read up to the latest message or a specific timestamp.

This is useful for:
- Clearing unread indicators after processing messages
- Tracking which messages have been handled by an agent
- Batch processing channels without manual intervention

Examples:
  # Mark entire channel as read
  slack mark "#general"
  slack mark C123ABC

  # Mark up to a specific message
  slack mark "#general" --ts 1704540600.123456

  # Mark multiple channels
  for ch in general random alerts; do slack mark "#$ch"; done`,
	Args: cobra.ExactArgs(1),
	RunE: runMark,
}

func init() {
	markCmd.Flags().StringVar(&markTimestamp, "ts", "", "mark as read up to this timestamp")
}

func runMark(cmd *cobra.Command, args []string) error {
	channel := args[0]

	cfg := config.Get()

	// Get authenticated client
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

	if err := api.MarkChannel(channelID, markTimestamp); err != nil {
		return err
	}

	if markTimestamp != "" {
		output.Success(fmt.Sprintf("Marked %s as read up to %s", channel, markTimestamp))
	} else {
		output.Success(fmt.Sprintf("Marked %s as read", channel))
	}

	return nil
}
