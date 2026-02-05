package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var draftsCmd = &cobra.Command{
	Use:   "drafts",
	Short: "Manage Slack drafts",
	Long: `Manage Slack drafts using the internal drafts API (requires xoxc token).

To use real drafts, first run 'slack drafts setup' to configure your xoxc credentials.
Without xoxc credentials, drafts fall back to scheduled messages (90 days out).

Getting xoxc credentials:
  1. Open Slack in your browser
  2. Open DevTools (F12) → Application → Local Storage → https://app.slack.com
  3. Find 'localConfig_v2' → expand → look for token starting with 'xoxc-'
  4. For the cookie: DevTools → Application → Cookies → app.slack.com → copy 'd' cookie value`,
}

var draftsSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure xoxc credentials for real drafts support",
	Long: `Configure xoxc token and d cookie for real Slack drafts support.

You need two things from your browser:
  1. xoxc token: DevTools → Application → Local Storage → localConfig_v2 → token
  2. d cookie: DevTools → Application → Cookies → app.slack.com → 'd' value

These credentials let slacli access Slack's internal drafts API.`,
	RunE: runDraftsSetup,
}

var draftsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check xoxc credentials status",
	RunE:  runDraftsStatus,
}

var draftsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List drafts",
	RunE:  runDraftsList,
}

var draftsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new draft",
	RunE:  runDraftsCreate,
}

var draftsShowCmd = &cobra.Command{
	Use:   "show <draft_id>",
	Short: "Show a draft's content",
	Args:  cobra.ExactArgs(1),
	RunE:  runDraftsShow,
}

var draftsEditCmd = &cobra.Command{
	Use:   "edit <draft_id>",
	Short: "Edit a draft's text",
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
	Short: "Send a draft immediately",
	Args:  cobra.ExactArgs(1),
	RunE:  runDraftsSend,
}

var (
	// Setup flags
	draftsSetupToken     string
	draftsSetupCookie    string
	draftsSetupWorkspace string

	// List flags
	draftsListChannel string

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
	draftsCmd.AddCommand(draftsSetupCmd)
	draftsCmd.AddCommand(draftsStatusCmd)
	draftsCmd.AddCommand(draftsListCmd)
	draftsCmd.AddCommand(draftsCreateCmd)
	draftsCmd.AddCommand(draftsShowCmd)
	draftsCmd.AddCommand(draftsEditCmd)
	draftsCmd.AddCommand(draftsDeleteCmd)
	draftsCmd.AddCommand(draftsSendCmd)

	// Setup flags
	draftsSetupCmd.Flags().StringVar(&draftsSetupToken, "token", "", "xoxc token (or set via prompt)")
	draftsSetupCmd.Flags().StringVar(&draftsSetupCookie, "cookie", "", "d cookie value (or set via prompt)")
	draftsSetupCmd.Flags().StringVar(&draftsSetupWorkspace, "workspace", "", "workspace subdomain (e.g., 'finn')")

	// List flags
	draftsListCmd.Flags().StringVar(&draftsListChannel, "channel", "", "filter by channel")

	// Create flags
	draftsCreateCmd.Flags().StringVar(&draftsCreateChannel, "channel", "", "target channel/DM (required)")
	draftsCreateCmd.Flags().StringVar(&draftsCreateText, "text", "", "draft text (or read from stdin)")
	draftsCreateCmd.Flags().StringVar(&draftsCreateThread, "thread", "", "reply to thread")
	draftsCreateCmd.MarkFlagRequired("channel")

	// Edit flags
	draftsEditCmd.Flags().StringVar(&draftsEditText, "text", "", "new text for draft")

	// Delete flags
	draftsDeleteCmd.Flags().BoolVar(&draftsDeleteForce, "force", false, "skip confirmation")
}

func runDraftsSetup(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	scanner := bufio.NewScanner(os.Stdin)

	// Get token
	token := draftsSetupToken
	if token == "" {
		fmt.Fprint(os.Stderr, "Enter xoxc token: ")
		if scanner.Scan() {
			token = strings.TrimSpace(scanner.Text())
		}
	}
	if token == "" {
		return fmt.Errorf("token required")
	}
	if !strings.HasPrefix(token, "xoxc-") {
		return fmt.Errorf("invalid token: must start with 'xoxc-'")
	}

	// Get cookie
	cookie := draftsSetupCookie
	if cookie == "" {
		fmt.Fprint(os.Stderr, "Enter d cookie value: ")
		if scanner.Scan() {
			cookie = strings.TrimSpace(scanner.Text())
		}
	}
	if cookie == "" {
		return fmt.Errorf("cookie required")
	}
	// Normalize cookie format
	cookie = strings.TrimPrefix(cookie, "d=")

	// Get workspace (optional but helps with edge cases)
	workspace := draftsSetupWorkspace
	if workspace == "" {
		fmt.Fprint(os.Stderr, "Enter workspace subdomain (e.g., 'finn', optional): ")
		if scanner.Scan() {
			workspace = strings.TrimSpace(scanner.Text())
		}
	}

	// Save credentials
	creds := &auth.XoxcCredentials{
		Token:     token,
		Cookie:    cookie,
		Workspace: workspace,
	}

	if err := auth.SaveXoxcCredentials(cfg, creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	// Test the credentials
	client, _, err := auth.GetXoxcClient(cfg)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	api := slack.NewXoxcAPI(client, workspace)
	info, err := api.TestAuth()
	if err != nil {
		// Remove invalid credentials
		os.Remove(cfg.XoxcCredentialsPath())
		return fmt.Errorf("credentials invalid: %w", err)
	}

	output.Success(fmt.Sprintf("Authenticated as %s (%s) in %s", info.UserName, info.UserID, info.TeamName))
	output.Info("Real drafts support enabled! Your drafts will sync with Slack's native drafts.")
	return nil
}

func runDraftsStatus(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	if !auth.HasXoxcCredentials(cfg) {
		output.Warn("xoxc credentials not configured")
		output.Info("Run 'slack drafts setup' to enable real drafts support")
		output.Info("Currently using scheduled messages as draft fallback")
		return nil
	}

	creds, err := auth.LoadXoxcCredentials(cfg)
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}

	// Test credentials
	client, _, err := auth.GetXoxcClient(cfg)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	api := slack.NewXoxcAPI(client, creds.Workspace)
	info, err := api.TestAuth()
	if err != nil {
		output.Warn("xoxc credentials expired or invalid")
		output.Info("Run 'slack drafts setup' to refresh credentials")
		return nil
	}

	fmt.Printf("Status: authenticated\n")
	fmt.Printf("User: %s (%s)\n", info.UserName, info.UserID)
	fmt.Printf("Team: %s (%s)\n", info.TeamName, info.TeamID)
	if creds.Workspace != "" {
		fmt.Printf("Workspace: %s\n", creds.Workspace)
	}
	fmt.Printf("Updated: %s\n", creds.UpdatedAt)
	return nil
}

func runDraftsList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	// Try xoxc first
	if auth.HasXoxcCredentials(cfg) {
		client, creds, err := auth.GetXoxcClient(cfg)
		if err == nil {
			api := slack.NewXoxcAPI(client, creds.Workspace)
			drafts, err := api.ListDrafts()
			if err == nil {
				// Filter by channel if specified
				if draftsListChannel != "" {
					channelID, err := api.ResolveChannel(draftsListChannel)
					if err != nil {
						return fmt.Errorf("resolve channel: %w", err)
					}
					filtered := make([]output.Draft, 0)
					for _, d := range drafts {
						if d.ChannelID == channelID {
							filtered = append(filtered, d)
						}
					}
					drafts = filtered
				}
				output.Print(output.DraftListResult{Drafts: drafts, Source: "xoxc"})
				return nil
			}
			output.Debug("xoxc drafts.list failed: %v, falling back to scheduled messages", err)
		}
	}

	// Fallback to scheduled messages
	return runDraftsListScheduled()
}

func runDraftsListScheduled() error {
	cfg := config.Get()

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	channelID := ""
	if draftsListChannel != "" {
		channelID, err = api.ResolveChannel(draftsListChannel)
		if err != nil {
			return fmt.Errorf("resolve channel: %w", err)
		}
	}

	messages, err := api.ListScheduledMessages(channelID)
	if err != nil {
		return fmt.Errorf("list scheduled messages: %w", err)
	}

	drafts := make([]output.Draft, len(messages))
	for i, m := range messages {
		drafts[i] = output.Draft{
			ID:        m.ID,
			ChannelID: m.ChannelID,
			Text:      m.Text,
			CreatedAt: time.Unix(m.DateCreated, 0).Format(time.RFC3339),
			UpdatedAt: time.Unix(m.PostAt, 0).Format(time.RFC3339),
		}
	}

	output.Print(output.DraftListResult{Drafts: drafts, Source: "scheduled"})
	return nil
}

func runDraftsCreate(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	text := draftsCreateText
	if text == "" {
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

	// Try xoxc first
	if auth.HasXoxcCredentials(cfg) {
		client, creds, err := auth.GetXoxcClient(cfg)
		if err == nil {
			api := slack.NewXoxcAPI(client, creds.Workspace)
			
			channelID, err := api.ResolveChannel(draftsCreateChannel)
			if err != nil {
				return fmt.Errorf("resolve channel: %w", err)
			}

			draftID, err := api.SaveDraft(channelID, text, draftsCreateThread, "")
			if err == nil {
				draft := output.Draft{
					ID:        draftID,
					ChannelID: channelID,
					Text:      text,
					ThreadTS:  draftsCreateThread,
					CreatedAt: time.Now().Format(time.RFC3339),
				}
				output.Print(draft)
				output.Success(fmt.Sprintf("Draft created! ID: %s (synced to Slack)", draftID))
				return nil
			}
			output.Debug("xoxc drafts.set failed: %v, falling back to scheduled messages", err)
		}
	}

	// Fallback to scheduled messages
	return runDraftsCreateScheduled(text)
}

func runDraftsCreateScheduled(text string) error {
	cfg := config.Get()

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	channelID, err := api.ResolveChannel(draftsCreateChannel)
	if err != nil {
		return fmt.Errorf("resolve channel: %w", err)
	}

	postAt := time.Now().Add(90 * 24 * time.Hour).Unix()

	msg, err := api.ScheduleMessage(channelID, text, postAt, draftsCreateThread)
	if err != nil {
		return fmt.Errorf("schedule message: %w", err)
	}

	draft := output.Draft{
		ID:        msg.ID,
		ChannelID: msg.ChannelID,
		Text:      text,
		ThreadTS:  draftsCreateThread,
		CreatedAt: time.Now().Format(time.RFC3339),
		UpdatedAt: time.Unix(msg.PostAt, 0).Format(time.RFC3339),
	}

	output.Print(draft)
	output.Success(fmt.Sprintf("Draft created (as scheduled message)! ID: %s", msg.ID))
	output.Info("Tip: Run 'slack drafts setup' for real drafts support")
	return nil
}

func runDraftsShow(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	draftID := args[0]

	// Try xoxc first
	if auth.HasXoxcCredentials(cfg) {
		client, creds, err := auth.GetXoxcClient(cfg)
		if err == nil {
			api := slack.NewXoxcAPI(client, creds.Workspace)
			drafts, err := api.ListDrafts()
			if err == nil {
				for _, d := range drafts {
					if d.ID == draftID {
						output.Print(d)
						return nil
					}
				}
			}
		}
	}

	// Fallback to scheduled messages
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)
	messages, err := api.ListScheduledMessages("")
	if err != nil {
		return fmt.Errorf("list scheduled messages: %w", err)
	}

	for _, m := range messages {
		if m.ID == draftID {
			draft := output.Draft{
				ID:        m.ID,
				ChannelID: m.ChannelID,
				Text:      m.Text,
				CreatedAt: time.Unix(m.DateCreated, 0).Format(time.RFC3339),
				UpdatedAt: time.Unix(m.PostAt, 0).Format(time.RFC3339),
			}
			output.Print(draft)
			return nil
		}
	}

	return fmt.Errorf("draft not found: %s", draftID)
}

func runDraftsEdit(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	draftID := args[0]

	text := draftsEditText
	if text == "" {
		var lines []string
		scanner := bufio.NewScanner(os.Stdin)
		fmt.Fprintln(os.Stderr, "Enter new text (Ctrl+D to finish):")
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		text = strings.Join(lines, "\n")
	}

	if text == "" {
		return fmt.Errorf("text required (use --text or stdin)")
	}

	// Try xoxc first - need to find the draft to get channelID
	if auth.HasXoxcCredentials(cfg) {
		client, creds, err := auth.GetXoxcClient(cfg)
		if err == nil {
			api := slack.NewXoxcAPI(client, creds.Workspace)
			drafts, err := api.ListDrafts()
			if err == nil {
				for _, d := range drafts {
					if d.ID == draftID {
						_, err := api.SaveDraft(d.ChannelID, text, d.ThreadTS, draftID)
						if err == nil {
							output.Success(fmt.Sprintf("Draft %s updated", draftID))
							return nil
						}
						output.Debug("xoxc drafts.set failed: %v", err)
						break
					}
				}
			}
		}
	}

	// For scheduled messages, we can't edit - need to delete and recreate
	output.Warn("Cannot edit scheduled message drafts. Delete and recreate instead.")
	return fmt.Errorf("edit not supported for scheduled message drafts")
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

	// Try xoxc first
	if auth.HasXoxcCredentials(cfg) {
		client, creds, err := auth.GetXoxcClient(cfg)
		if err == nil {
			api := slack.NewXoxcAPI(client, creds.Workspace)
			// Need to find draft to get channelID
			drafts, err := api.ListDrafts()
			if err == nil {
				for _, d := range drafts {
					if d.ID == draftID {
						if err := api.DeleteDraft(d.ChannelID, draftID); err != nil {
							output.Debug("xoxc delete failed: %v", err)
							// Return the error directly for xoxc drafts
							return fmt.Errorf("delete draft: %w", err)
						}
						output.Success("Draft deleted")
						return nil
					}
				}
				// Draft not found in xoxc list, fall through to scheduled messages
				output.Debug("Draft %s not found in xoxc drafts, trying scheduled messages", draftID)
			} else {
				output.Debug("xoxc ListDrafts failed: %v", err)
			}
		}
	}

	// Fallback to scheduled messages
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	messages, err := api.ListScheduledMessages("")
	if err != nil {
		return fmt.Errorf("list scheduled messages: %w", err)
	}

	var channelID string
	for _, m := range messages {
		if m.ID == draftID {
			channelID = m.ChannelID
			break
		}
	}

	if channelID == "" {
		return fmt.Errorf("draft not found: %s", draftID)
	}

	if err := api.DeleteScheduledMessage(channelID, draftID); err != nil {
		return fmt.Errorf("delete scheduled message: %w", err)
	}

	output.Success("Draft deleted")
	return nil
}

func runDraftsSend(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	draftID := args[0]

	// For xoxc drafts, we need to get the draft content and send it
	// The drafts API doesn't have a "publish" endpoint, so we:
	// 1. Get the draft content
	// 2. Send as regular message
	// 3. Delete the draft

	if auth.HasXoxcCredentials(cfg) {
		client, creds, err := auth.GetXoxcClient(cfg)
		if err == nil {
			api := slack.NewXoxcAPI(client, creds.Workspace)
			drafts, err := api.ListDrafts()
			if err == nil {
				for _, d := range drafts {
					if d.ID == draftID {
						// Use regular API to send message
						oauthClient, err := auth.GetClient(cfg)
						if err != nil {
							return fmt.Errorf("oauth auth required to send: %w", err)
						}
						
						oauthAPI := slack.NewAPI(oauthClient)
						result, err := oauthAPI.SendMessage(d.ChannelID, d.Text, d.ThreadTS)
						if err != nil {
							return fmt.Errorf("send message: %w", err)
						}

						// Delete the draft
						_ = api.DeleteDraft(d.ChannelID, draftID)

						output.Print(result)
						output.Success("Draft sent!")
						return nil
					}
				}
			}
		}
	}

	// Fallback to scheduled messages
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	messages, err := api.ListScheduledMessages("")
	if err != nil {
		return fmt.Errorf("list scheduled messages: %w", err)
	}

	var draft *slack.ScheduledMessage
	for _, m := range messages {
		if m.ID == draftID {
			draft = &m
			break
		}
	}

	if draft == nil {
		return fmt.Errorf("draft not found: %s", draftID)
	}

	// Delete the scheduled message
	if err := api.DeleteScheduledMessage(draft.ChannelID, draftID); err != nil {
		return fmt.Errorf("delete scheduled message: %w", err)
	}

	// Send immediately
	result, err := api.SendMessage(draft.ChannelID, draft.Text, "")
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	output.Print(result)
	output.Success("Draft sent!")
	return nil
}
