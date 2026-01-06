package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var draftsCmd = &cobra.Command{
	Use:   "drafts",
	Short: "Manage Slack drafts",
}

var draftsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved drafts",
	RunE:  runDraftsList,
}

var draftsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new draft",
	RunE:  runDraftsCreate,
}

var draftsEditCmd = &cobra.Command{
	Use:   "edit <draft_id>",
	Short: "Edit an existing draft",
	Args:  cobra.ExactArgs(1),
	RunE:  runDraftsEdit,
}

var draftsDeleteCmd = &cobra.Command{
	Use:   "delete <draft_id>",
	Short: "Delete a draft",
	Args:  cobra.ExactArgs(1),
	RunE:  runDraftsDelete,
}

var draftsSendCmd = &cobra.Command{
	Use:   "send <draft_id>",
	Short: "Send a draft as a message",
	Args:  cobra.ExactArgs(1),
	RunE:  runDraftsSend,
}

var (
	// List flags
	draftsListLimit int

	// Create flags
	draftsCreateChannel string
	draftsCreateText    string
	draftsCreateThread  string

	// Edit flags
	draftsEditText string

	// Delete flags
	draftsDeleteForce bool
)

func init() {
	draftsCmd.AddCommand(draftsListCmd)
	draftsCmd.AddCommand(draftsCreateCmd)
	draftsCmd.AddCommand(draftsEditCmd)
	draftsCmd.AddCommand(draftsDeleteCmd)
	draftsCmd.AddCommand(draftsSendCmd)

	// List flags
	draftsListCmd.Flags().IntVar(&draftsListLimit, "limit", 20, "max results")

	// Create flags
	draftsCreateCmd.Flags().StringVar(&draftsCreateChannel, "channel", "", "target channel/DM (required)")
	draftsCreateCmd.Flags().StringVar(&draftsCreateText, "text", "", "draft text (or read from stdin)")
	draftsCreateCmd.Flags().StringVar(&draftsCreateThread, "thread", "", "reply to thread")
	draftsCreateCmd.MarkFlagRequired("channel")

	// Edit flags
	draftsEditCmd.Flags().StringVar(&draftsEditText, "text", "", "new text (or stdin)")

	// Delete flags
	draftsDeleteCmd.Flags().BoolVar(&draftsDeleteForce, "force", false, "skip confirmation")
}

func runDraftsList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)
	drafts, err := api.ListDrafts(draftsListLimit)
	if err != nil {
		return fmt.Errorf("list drafts: %w", err)
	}

	output.Print(output.DraftListResult{Drafts: drafts})
	return nil
}

func runDraftsCreate(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	text := draftsCreateText
	if text == "" {
		// Read from stdin
		var lines []string
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		text = strings.Join(lines, "\n")
	}

	if text == "" {
		return fmt.Errorf("draft text required (use --text or stdin)")
	}

	api := slack.NewAPI(client)

	// Resolve channel
	channelID, err := api.ResolveChannel(draftsCreateChannel)
	if err != nil {
		return fmt.Errorf("resolve channel: %w", err)
	}

	draft, err := api.CreateDraft(channelID, text, draftsCreateThread)
	if err != nil {
		return fmt.Errorf("create draft: %w", err)
	}

	output.Print(draft)
	return nil
}

func runDraftsEdit(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	draftID := args[0]

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	text := draftsEditText
	if text == "" {
		// Read from stdin
		var lines []string
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		text = strings.Join(lines, "\n")
	}

	if text == "" {
		return fmt.Errorf("new text required (use --text or stdin)")
	}

	api := slack.NewAPI(client)
	draft, err := api.EditDraft(draftID, text)
	if err != nil {
		return fmt.Errorf("edit draft: %w", err)
	}

	output.Print(draft)
	return nil
}

func runDraftsDelete(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	draftID := args[0]

	if !draftsDeleteForce {
		fmt.Fprintf(os.Stderr, "Delete draft %s? [y/N] ", draftID)
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			return fmt.Errorf("cancelled")
		}
	}

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)
	if err := api.DeleteDraft(draftID); err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}

	output.Success("Draft deleted")
	return nil
}

func runDraftsSend(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	draftID := args[0]

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)
	result, err := api.SendDraft(draftID)
	if err != nil {
		return fmt.Errorf("send draft: %w", err)
	}

	output.Print(result)
	return nil
}
