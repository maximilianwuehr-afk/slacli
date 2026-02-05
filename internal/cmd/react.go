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
	reactRemove bool
)

var reactCmd = &cobra.Command{
	Use:   "react <channel> <timestamp> <emoji>",
	Short: "Add or remove a reaction from a message",
	Long: `Add or remove an emoji reaction from a message.

Examples:
  # Add a reaction
  slack react "#general" 1704540600.123456 :thumbsup:
  slack react "#general" 1704540600.123456 thumbsup
  slack react C123ABC 1704540600.123456 white_check_mark

  # Remove a reaction
  slack react --remove "#general" 1704540600.123456 :thumbsup:

  # React to acknowledge a message (agent pattern)
  slack react "#alerts" 1704540600.123456 eyes`,
	Args: cobra.ExactArgs(3),
	RunE: runReact,
}

func init() {
	reactCmd.Flags().BoolVarP(&reactRemove, "remove", "r", false, "remove reaction instead of adding")
}

func runReact(cmd *cobra.Command, args []string) error {
	channel := args[0]
	timestamp := args[1]
	emoji := args[2]

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

	if reactRemove {
		if err := api.RemoveReaction(channelID, timestamp, emoji); err != nil {
			return err
		}
		output.Success(fmt.Sprintf("Removed :%s: from message", emoji))
	} else {
		if err := api.AddReaction(channelID, timestamp, emoji); err != nil {
			return err
		}
		output.Success(fmt.Sprintf("Added :%s: to message", emoji))
	}

	return nil
}

// reactionsCmd lists reactions on a message
var reactionsCmd = &cobra.Command{
	Use:   "reactions <channel> <timestamp>",
	Short: "List reactions on a message",
	Long: `List all reactions on a specific message.

Examples:
  slack reactions "#general" 1704540600.123456
  slack reactions C123ABC 1704540600.123456 --json`,
	Args: cobra.ExactArgs(2),
	RunE: runReactions,
}

// reactionsCmd init is empty - registered in root.go

func runReactions(cmd *cobra.Command, args []string) error {
	channel := args[0]
	timestamp := args[1]

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

	reactions, err := api.GetReactions(channelID, timestamp)
	if err != nil {
		return err
	}

	if len(reactions) == 0 {
		output.Info("No reactions on this message")
		return nil
	}

	output.Print(reactions)
	return nil
}
