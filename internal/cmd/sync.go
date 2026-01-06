package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
	"slacli/internal/sync"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync channels and messages to local database",
	Long:  `Pull channels, users, and messages from Slack to local SQLite database.`,
	RunE:  runSync,
}

var (
	syncFull         bool
	syncChannelsOnly bool
	syncDays         int
	syncFollow       bool
)

func init() {
	syncCmd.Flags().BoolVar(&syncFull, "full", false, "full resync (ignore cursors)")
	syncCmd.Flags().BoolVar(&syncChannelsOnly, "channels-only", false, "only sync channel list")
	syncCmd.Flags().IntVar(&syncDays, "days", 30, "sync last N days of messages")
	syncCmd.Flags().BoolVar(&syncFollow, "follow", false, "continuous sync loop")
}

func runSync(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	// Ensure authenticated
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	opts := sync.Options{
		Full:         syncFull,
		ChannelsOnly: syncChannelsOnly,
		Days:         syncDays,
		Follow:       syncFollow,
	}

	syncer := sync.New(cfg, client)

	if syncFollow {
		output.Info("Starting continuous sync (Ctrl+C to stop)...")
		return syncer.Follow(opts)
	}

	result, err := syncer.Run(opts)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	output.Print(result)
	return nil
}
