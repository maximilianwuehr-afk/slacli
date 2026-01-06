package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
)

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage users",
}

var usersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace users",
	RunE:  runUsersList,
}

var (
	usersSearch string
	usersLimit  int
)

func init() {
	usersCmd.AddCommand(usersListCmd)

	usersListCmd.Flags().StringVar(&usersSearch, "search", "", "filter by name/email")
	usersListCmd.Flags().IntVar(&usersLimit, "limit", 100, "max results")
}

func runUsersList(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer store.Close()

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
