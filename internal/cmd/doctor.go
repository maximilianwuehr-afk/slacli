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

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics",
	Long:  `Check auth status, database integrity, sync state, and API connectivity.`,
	RunE:  runDoctor,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	checks := []output.DoctorCheck{}

	// Check 1: Config directory
	configCheck := output.DoctorCheck{
		Name:   "Config directory",
		Status: "ok",
	}
	if err := config.EnsureDir(cfg); err != nil {
		configCheck.Status = "error"
		configCheck.Message = err.Error()
	} else {
		configCheck.Message = cfg.StoreDir
	}
	checks = append(checks, configCheck)

	// Check 2: Auth status
	authCheck := output.DoctorCheck{
		Name:   "Authentication",
		Status: "ok",
	}
	creds, err := auth.LoadCredentials(cfg)
	if err != nil {
		authCheck.Status = "error"
		authCheck.Message = "Not authenticated (run: slacli auth)"
	} else if creds.IsExpired() {
		authCheck.Status = "warning"
		authCheck.Message = fmt.Sprintf("Token expired at %s (run: slacli auth --refresh)", creds.ExpiresAt)
	} else {
		authCheck.Message = fmt.Sprintf("Valid until %s", creds.ExpiresAt)
	}
	checks = append(checks, authCheck)

	// Check 3: Database
	dbCheck := output.DoctorCheck{
		Name:   "Database",
		Status: "ok",
	}
	store, err := db.Open(cfg)
	if err != nil {
		dbCheck.Status = "error"
		dbCheck.Message = err.Error()
	} else {
		stats, err := store.Stats()
		if err != nil {
			dbCheck.Status = "error"
			dbCheck.Message = err.Error()
		} else {
			dbCheck.Message = fmt.Sprintf("%d channels, %d messages, %d users (%.1f MB)",
				stats.ChannelCount, stats.MessageCount, stats.UserCount, float64(stats.SizeBytes)/(1024*1024))
		}
		_ = store.Close()
	}
	checks = append(checks, dbCheck)

	// Check 4: Sync state
	syncCheck := output.DoctorCheck{
		Name:   "Sync state",
		Status: "ok",
	}
	if store != nil {
		state, err := db.LoadSyncState(cfg)
		if err != nil {
			syncCheck.Status = "warning"
			syncCheck.Message = "Never synced (run: slacli sync)"
		} else {
			syncCheck.Message = fmt.Sprintf("Last sync: %s", state.LastSync)
		}
	}
	checks = append(checks, syncCheck)

	// Check 5: API connectivity
	apiCheck := output.DoctorCheck{
		Name:   "Slack API",
		Status: "ok",
	}
	if creds != nil && !creds.IsExpired() {
		client, _ := auth.GetClient(cfg)
		if client != nil {
			api := slack.NewAPI(client)
			if err := api.Test(); err != nil {
				apiCheck.Status = "error"
				apiCheck.Message = err.Error()
			} else {
				apiCheck.Message = "Connected"
			}
		}
	} else {
		apiCheck.Status = "skip"
		apiCheck.Message = "Skipped (not authenticated)"
	}
	checks = append(checks, apiCheck)

	output.Print(output.DoctorResult{Checks: checks})
	return nil
}
