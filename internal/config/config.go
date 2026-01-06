package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	StoreDir         string `json:"store_dir"`
	DefaultWorkspace string `json:"default_workspace,omitempty"`
	SyncDays         int    `json:"sync_days"`
	RetentionDays    int    `json:"retention_days"`
	AutoPrune        bool   `json:"auto_prune"`
	VacuumAfterPrune bool   `json:"vacuum_after_prune"`
}

var cfg *Config

// Init initializes the configuration
func Init(storeDir string) error {
	cfg = &Config{
		SyncDays:         30,
		RetentionDays:    90,
		AutoPrune:        false,
		VacuumAfterPrune: true,
	}

	// Determine store directory
	if storeDir != "" {
		cfg.StoreDir = storeDir
	} else if envDir := os.Getenv("SLACLI_STORE"); envDir != "" {
		cfg.StoreDir = envDir
	} else if viperDir := viper.GetString("store"); viperDir != "" {
		cfg.StoreDir = viperDir
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		cfg.StoreDir = filepath.Join(home, ".slacli")
	}

	// Ensure directory exists
	if err := EnsureDir(cfg); err != nil {
		return err
	}

	// Load config file if exists
	configPath := filepath.Join(cfg.StoreDir, "config.json")
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}

	// Apply viper overrides
	if v := viper.GetInt("sync_days"); v > 0 {
		cfg.SyncDays = v
	}
	if v := viper.GetInt("retention_days"); v > 0 {
		cfg.RetentionDays = v
	}
	if viper.IsSet("auto_prune") {
		cfg.AutoPrune = viper.GetBool("auto_prune")
	}

	return nil
}

// Get returns the current configuration
func Get() *Config {
	if cfg == nil {
		// Return defaults if not initialized
		home, _ := os.UserHomeDir()
		return &Config{
			StoreDir:         filepath.Join(home, ".slacli"),
			SyncDays:         30,
			RetentionDays:    90,
			AutoPrune:        false,
			VacuumAfterPrune: true,
		}
	}
	return cfg
}

// EnsureDir ensures the store directory exists with correct permissions
func EnsureDir(c *Config) error {
	if err := os.MkdirAll(c.StoreDir, 0700); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	return nil
}

// Save saves the configuration to disk
func Save(c *Config) error {
	configPath := filepath.Join(c.StoreDir, "config.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// CredentialsPath returns the path to credentials file
func (c *Config) CredentialsPath() string {
	return filepath.Join(c.StoreDir, "credentials.json")
}

// DatabasePath returns the path to the SQLite database
func (c *Config) DatabasePath() string {
	return filepath.Join(c.StoreDir, "slacli.db")
}

// SyncStatePath returns the path to sync state file
func (c *Config) SyncStatePath() string {
	return filepath.Join(c.StoreDir, "sync_state.json")
}
