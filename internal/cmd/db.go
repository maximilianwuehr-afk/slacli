package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database management",
}

var dbStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show database statistics",
	RunE:  runDBStats,
}

var dbPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete old messages",
	RunE:  runDBPrune,
}

var dbVacuumCmd = &cobra.Command{
	Use:   "vacuum",
	Short: "Reclaim disk space",
	RunE:  runDBVacuum,
}

var dbResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Wipe database and start fresh",
	RunE:  runDBReset,
}

var (
	// Prune flags
	pruneOlderThan int
	pruneChannel   string
	pruneDryRun    bool
	pruneForce     bool

	// Reset flags
	resetForce    bool
	resetKeepAuth bool
)

func init() {
	dbCmd.AddCommand(dbStatsCmd)
	dbCmd.AddCommand(dbPruneCmd)
	dbCmd.AddCommand(dbVacuumCmd)
	dbCmd.AddCommand(dbResetCmd)

	// Prune flags
	dbPruneCmd.Flags().IntVar(&pruneOlderThan, "older-than", 90, "delete messages older than N days")
	dbPruneCmd.Flags().StringVar(&pruneChannel, "channel", "", "prune only specific channel")
	dbPruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "show what would be deleted")
	dbPruneCmd.Flags().BoolVar(&pruneForce, "force", false, "skip confirmation")

	// Reset flags
	dbResetCmd.Flags().BoolVar(&resetForce, "force", false, "skip confirmation (required)")
	dbResetCmd.Flags().BoolVar(&resetKeepAuth, "keep-auth", false, "keep credentials, wipe only data")
}

func runDBStats(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer store.Close()

	stats, err := store.Stats()
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}

	output.Print(stats)
	return nil
}

func runDBPrune(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer store.Close()

	opts := db.PruneOptions{
		OlderThanDays: pruneOlderThan,
		Channel:       pruneChannel,
		DryRun:        pruneDryRun,
	}

	if pruneDryRun {
		result, err := store.PrunePreview(opts)
		if err != nil {
			return fmt.Errorf("prune preview: %w", err)
		}
		output.Print(result)
		return nil
	}

	if !pruneForce {
		fmt.Fprintf(os.Stderr, "Delete messages older than %d days? [y/N] ", pruneOlderThan)
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			return fmt.Errorf("cancelled")
		}
	}

	result, err := store.Prune(opts)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	output.Print(result)
	return nil
}

func runDBVacuum(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	store, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer store.Close()

	sizeBefore, err := store.Size()
	if err != nil {
		return fmt.Errorf("get size: %w", err)
	}

	if err := store.Vacuum(); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}

	sizeAfter, err := store.Size()
	if err != nil {
		return fmt.Errorf("get size: %w", err)
	}

	output.Print(output.VacuumResult{
		SizeBefore: sizeBefore,
		SizeAfter:  sizeAfter,
		Reclaimed:  sizeBefore - sizeAfter,
	})
	return nil
}

func runDBReset(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	if !resetForce {
		fmt.Fprintf(os.Stderr, "This will DELETE ALL DATA. Are you sure? [y/N] ")
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			return fmt.Errorf("cancelled")
		}
	}

	if err := db.Reset(cfg, resetKeepAuth); err != nil {
		return fmt.Errorf("reset: %w", err)
	}

	output.Success("Database reset complete")
	return nil
}
