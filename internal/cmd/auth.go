package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with Slack via OAuth",
	Long:  `Opens browser for Slack OAuth flow, stores token locally.`,
	RunE:  runAuth,
}

var (
	authRefresh bool
	authStatus  bool
	authLogout  bool
)

func init() {
	authCmd.Flags().BoolVar(&authRefresh, "refresh", false, "force token refresh")
	authCmd.Flags().BoolVar(&authStatus, "status", false, "check auth status without re-auth")
	authCmd.Flags().BoolVar(&authLogout, "logout", false, "revoke token and delete credentials")
}

func runAuth(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	if authLogout {
		if err := auth.Logout(cfg); err != nil {
			return fmt.Errorf("logout failed: %w", err)
		}
		output.Success("Logged out successfully")
		return nil
	}

	if authStatus {
		status, err := auth.Status(cfg)
		if err != nil {
			return fmt.Errorf("status check failed: %w", err)
		}
		output.Print(status)
		return nil
	}

	if authRefresh {
		if err := auth.Refresh(cfg); err != nil {
			return fmt.Errorf("refresh failed: %w", err)
		}
		output.Success("Token refreshed successfully")
		return nil
	}

	// Full OAuth flow
	if err := auth.Login(cfg); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	output.Success("Authenticated successfully")
	return nil
}
