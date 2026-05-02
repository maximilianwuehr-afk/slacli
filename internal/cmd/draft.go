package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var draftCmd = &cobra.Command{
	Use:   "draft <channel> [text]",
	Short: "Create a native Slack draft without sending it",
	Long: `Create a native Slack draft without sending it.

Channel can be:
  - #channel-name
  - @username
  - user@email.com (for DM)
  - Channel ID (C..., D..., G...)

This command requires xoxc credentials configured with 'slacli drafts setup'.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runDraft,
}

var (
	draftThread string
	draftStdin  bool
)

func init() {
	draftCmd.Flags().StringVar(&draftThread, "thread", "", "create a thread reply draft")
	draftCmd.Flags().BoolVar(&draftStdin, "stdin", false, "read draft text from stdin")
}

func runDraft(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	client, creds, err := auth.GetXoxcClient(cfg)
	if err != nil {
		return fmt.Errorf("xoxc auth required: %w", err)
	}

	text := ""
	if len(args) > 1 {
		text = args[1]
	}
	if draftStdin {
		text, err = readDraftText("", true)
		if err != nil {
			return err
		}
	}
	if text == "" {
		return fmt.Errorf("draft text required (pass text or use --stdin)")
	}

	api := slack.NewXoxcAPI(client, creds.Workspace)
	draft, updated, err := saveNativeDraft(api, args[0], text, draftThread)
	if err != nil {
		return err
	}

	output.Print(draft)
	if updated {
		output.Success(fmt.Sprintf("Draft updated: %s", draft.ID))
	} else {
		output.Success(fmt.Sprintf("Draft created: %s", draft.ID))
	}
	return nil
}
