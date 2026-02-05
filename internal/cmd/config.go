package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/config"
	"slacli/internal/output"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
	Long:  `View and modify slacli configuration settings.`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Get()
		output.Print(cfg)
		return nil
	},
}

var configWhitelistCmd = &cobra.Command{
	Use:   "whitelist",
	Short: "Manage channel whitelist (channels always synced)",
}

var configWhitelistAddCmd = &cobra.Command{
	Use:   "add <channel>",
	Short: "Add channel to whitelist",
	Long:  `Add a channel to the whitelist. Use channel name (with or without #) or channel ID.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Get()
		channel := args[0]

		if cfg.AddWhitelistChannel(channel) {
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			output.Success(fmt.Sprintf("Added %s to whitelist", channel))
		} else {
			output.Info("%s is already in whitelist", channel)
		}
		return nil
	},
}

var configWhitelistRemoveCmd = &cobra.Command{
	Use:   "remove <channel>",
	Short: "Remove channel from whitelist",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Get()
		channel := args[0]

		if cfg.RemoveWhitelistChannel(channel) {
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			output.Success(fmt.Sprintf("Removed %s from whitelist", channel))
		} else {
			output.Info("%s was not in whitelist", channel)
		}
		return nil
	},
}

var configWhitelistListCmd = &cobra.Command{
	Use:   "list",
	Short: "List whitelisted channels",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Get()

		if len(cfg.WhitelistChannels) == 0 {
			output.Info("No channels whitelisted")
			return nil
		}

		result := struct {
			Channels []string `json:"channels"`
		}{
			Channels: cfg.WhitelistChannels,
		}
		output.Print(result)
		return nil
	},
}

func init() {
	configWhitelistCmd.AddCommand(configWhitelistAddCmd)
	configWhitelistCmd.AddCommand(configWhitelistRemoveCmd)
	configWhitelistCmd.AddCommand(configWhitelistListCmd)

	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configWhitelistCmd)
}
