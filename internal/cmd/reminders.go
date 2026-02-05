package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var (
	reminderTime string
	reminderUser string
)

var remindersCmd = &cobra.Command{
	Use:   "reminders",
	Short: "Manage Slack reminders",
	Long: `Create, list, complete, and delete Slack reminders.

Reminders are useful for:
- Scheduling follow-ups
- Agent task queuing
- Time-based notifications`,
}

var remindersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all reminders",
	Long: `List all reminders for the authenticated user.

Examples:
  slack reminders list
  slack reminders list --json`,
	RunE: runRemindersList,
}

var remindersAddCmd = &cobra.Command{
	Use:   "add <text>",
	Short: "Create a new reminder",
	Long: `Create a new reminder with natural language time parsing.

Time formats supported:
  - Natural language: "in 2 hours", "tomorrow at 10am", "next monday"
  - ISO 8601: "2024-01-15T10:00:00"
  - Unix timestamp: "1704540600"

Examples:
  slack reminders add "Review PR" --time "in 2 hours"
  slack reminders add "Follow up with Alice" --time "tomorrow at 10am"
  slack reminders add "Team standup" --time "next monday 9:30am"
  slack reminders add "Check build" --time "1704540600"
  
  # Remind another user (requires admin scope)
  slack reminders add "Submit report" --time "friday 5pm" --user @bob`,
	Args: cobra.ExactArgs(1),
	RunE: runRemindersAdd,
}

var remindersCompleteCmd = &cobra.Command{
	Use:   "complete <reminder-id>",
	Short: "Mark a reminder as complete",
	Long: `Mark a reminder as complete.

Get reminder IDs from 'slack reminders list --json'.

Examples:
  slack reminders complete Rm12345ABC`,
	Args: cobra.ExactArgs(1),
	RunE: runRemindersComplete,
}

var remindersDeleteCmd = &cobra.Command{
	Use:   "delete <reminder-id>",
	Short: "Delete a reminder",
	Long: `Delete a reminder.

Get reminder IDs from 'slack reminders list --json'.

Examples:
  slack reminders delete Rm12345ABC`,
	Args: cobra.ExactArgs(1),
	RunE: runRemindersDelete,
}

var remindersInfoCmd = &cobra.Command{
	Use:   "info <reminder-id>",
	Short: "Get info about a reminder",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemindersInfo,
}

func init() {
	remindersCmd.AddCommand(remindersListCmd)
	remindersCmd.AddCommand(remindersAddCmd)
	remindersCmd.AddCommand(remindersCompleteCmd)
	remindersCmd.AddCommand(remindersDeleteCmd)
	remindersCmd.AddCommand(remindersInfoCmd)

	remindersAddCmd.Flags().StringVar(&reminderTime, "time", "", "when to trigger (required)")
	remindersAddCmd.Flags().StringVar(&reminderUser, "user", "", "user to remind (default: self)")
	remindersAddCmd.MarkFlagRequired("time")
}

func runRemindersList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	reminders, err := api.ListReminders()
	if err != nil {
		return err
	}

	if len(reminders) == 0 {
		output.Info("No reminders")
		return nil
	}

	// Format for display
	type displayReminder struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		Time      string `json:"time"`
		Completed bool   `json:"completed"`
		Recurring bool   `json:"recurring"`
	}

	displayed := make([]displayReminder, 0, len(reminders))
	for _, r := range reminders {
		displayed = append(displayed, displayReminder{
			ID:        r.ID,
			Text:      r.Text,
			Time:      time.Unix(r.Time, 0).Format(time.RFC3339),
			Completed: r.CompleteTS > 0,
			Recurring: r.Recurring,
		})
	}

	output.Print(displayed)
	return nil
}

func runRemindersAdd(cmd *cobra.Command, args []string) error {
	text := args[0]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	// Resolve user if provided
	var userID string
	if reminderUser != "" {
		// Could be email or @mention
		user := reminderUser
		if user[0] == '@' {
			user = user[1:]
		}
		// Try as email first
		if userInfo, err := api.GetUserByEmail(user); err == nil {
			userID = userInfo.ID
		}
	}

	reminder, err := api.AddReminder(text, reminderTime, userID)
	if err != nil {
		return err
	}

	output.Success(fmt.Sprintf("Created reminder %s for %s", reminder.ID, time.Unix(reminder.Time, 0).Format("Mon Jan 2 15:04")))
	output.Print(reminder)
	return nil
}

func runRemindersComplete(cmd *cobra.Command, args []string) error {
	reminderID := args[0]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	if err := api.CompleteReminder(reminderID); err != nil {
		return err
	}

	output.Success(fmt.Sprintf("Marked reminder %s as complete", reminderID))
	return nil
}

func runRemindersDelete(cmd *cobra.Command, args []string) error {
	reminderID := args[0]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	if err := api.DeleteReminder(reminderID); err != nil {
		return err
	}

	output.Success(fmt.Sprintf("Deleted reminder %s", reminderID))
	return nil
}

func runRemindersInfo(cmd *cobra.Command, args []string) error {
	reminderID := args[0]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	reminder, err := api.GetReminderInfo(reminderID)
	if err != nil {
		return err
	}

	output.Print(reminder)
	return nil
}
