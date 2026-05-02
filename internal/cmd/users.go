package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage users",
	Long: `List, lookup, and get info about workspace users.

Examples:
  slack users list
  slack users info U12345ABC
  slack users lookup alice@company.com
  slack users presence @alice`,
}

var usersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace users",
	Long: `List users from the local database.

Run 'slack sync' first to populate the database.

Examples:
  slack users list
  slack users list --search "alice"
  slack users list --json`,
	RunE: runUsersList,
}

var usersInfoCmd = &cobra.Command{
	Use:   "info <user-id>",
	Short: "Get detailed info about a user",
	Long: `Get detailed information about a user by ID.

Examples:
  slack users info U12345ABC
  slack users info U12345ABC --json`,
	Args: cobra.ExactArgs(1),
	RunE: runUsersInfo,
}

var usersLookupCmd = &cobra.Command{
	Use:   "lookup <email>",
	Short: "Look up a user by email address",
	Long: `Look up a user by their email address.

Examples:
  slack users lookup alice@company.com
  slack users lookup bob@example.org --json`,
	Args: cobra.ExactArgs(1),
	RunE: runUsersLookup,
}

var usersPresenceCmd = &cobra.Command{
	Use:   "presence [user-id]",
	Short: "Get or set user presence",
	Long: `Get a user's presence status or set your own.

Without arguments, returns your own presence.
With a user ID, returns that user's presence.
With --set, changes your own presence.

Examples:
  # Get your presence
  slack users presence

  # Get another user's presence
  slack users presence U12345ABC
  slack users presence @alice

  # Set your presence
  slack users presence --set away
  slack users presence --set auto`,
	Args: cobra.MaximumNArgs(1),
	RunE: runUsersPresence,
}

var (
	usersSearch string
	usersLimit  int
	presenceSet string
)

func init() {
	usersCmd.AddCommand(usersListCmd)
	usersCmd.AddCommand(usersInfoCmd)
	usersCmd.AddCommand(usersLookupCmd)
	usersCmd.AddCommand(usersPresenceCmd)

	usersListCmd.Flags().StringVar(&usersSearch, "search", "", "filter by name/email")
	usersListCmd.Flags().IntVar(&usersLimit, "limit", 100, "max results")

	usersPresenceCmd.Flags().StringVar(&presenceSet, "set", "", "set presence: 'away' or 'auto'")
}

func runUsersList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() { _ = store.Close() }()

	opts := db.UserListOptions{
		Search: usersSearch,
		Limit:  usersLimit,
	}

	users, err := store.ListUsers(opts)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}

	output.Print(output.UserListResult{Users: users})
	return nil
}

func runUsersInfo(cmd *cobra.Command, args []string) error {
	userID := args[0]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	user, err := api.GetUserInfo(userID)
	if err != nil {
		return err
	}

	// Format for display
	type userDisplay struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		RealName    string `json:"real_name"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		IsBot       bool   `json:"is_bot"`
		Deleted     bool   `json:"deleted"`
		Avatar      string `json:"avatar"`
	}

	output.Print(userDisplay{
		ID:          user.ID,
		Name:        user.Name,
		RealName:    user.RealName,
		DisplayName: user.Profile.DisplayName,
		Email:       user.Profile.Email,
		IsBot:       user.IsBot,
		Deleted:     user.Deleted,
		Avatar:      user.Profile.Image48,
	})
	return nil
}

func runUsersLookup(cmd *cobra.Command, args []string) error {
	email := args[0]

	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	user, err := api.GetUserByEmail(email)
	if err != nil {
		return err
	}

	// Format for display
	type userDisplay struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		RealName    string `json:"real_name"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		IsBot       bool   `json:"is_bot"`
	}

	output.Print(userDisplay{
		ID:          user.ID,
		Name:        user.Name,
		RealName:    user.RealName,
		DisplayName: user.Profile.DisplayName,
		Email:       user.Profile.Email,
		IsBot:       user.IsBot,
	})
	return nil
}

func runUsersPresence(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	api := slack.NewAPI(client)

	// Set presence if requested
	if presenceSet != "" {
		if presenceSet != "away" && presenceSet != "auto" {
			return fmt.Errorf("presence must be 'away' or 'auto'")
		}
		if err := api.SetUserPresence(presenceSet); err != nil {
			return err
		}
		output.Success(fmt.Sprintf("Set presence to %s", presenceSet))
		return nil
	}

	// Get presence
	var userID string
	if len(args) > 0 {
		userID = args[0]
		// Resolve if it's a mention
		if userID[0] == '@' {
			// Try to look up by name - for now just use the ID format
			userID = userID[1:]
		}
	} else {
		// Get current user's ID
		authInfo, err := api.GetAuthInfo()
		if err != nil {
			return err
		}
		userID = authInfo.UserID
	}

	presence, err := api.GetUserPresence(userID)
	if err != nil {
		return err
	}

	output.Print(presence)
	return nil
}
