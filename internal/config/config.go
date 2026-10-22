package config

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	StoreDir          string   `json:"store_dir"`
	DefaultWorkspace  string   `json:"default_workspace,omitempty"`
	SyncDays          int      `json:"sync_days"`
	RetentionDays     int      `json:"retention_days"`
	AutoPrune         bool     `json:"auto_prune"`
	VacuumAfterPrune  bool     `json:"vacuum_after_prune"`
	WhitelistChannels []string `json:"whitelist_channels,omitempty"` // Channels to always sync (names or IDs)
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

// SecureDir creates the store directory and limits access to the current user.
func SecureDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect store dir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("store path is not a directory: %s", path)
	}
	if err := os.Chmod(path, 0700); err != nil {
		return fmt.Errorf("secure store dir: %w", err)
	}
	return nil
}

// SecureFile limits an existing regular file to the current user. A missing
// file is left missing.
func SecureFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("secure file: %w", err)
	}
	return nil
}

// EnsurePrivateFile creates a regular file if needed and sets its mode to 0600.
func EnsurePrivateFile(path string) error {
	if err := SecureFile(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("create private file: %w", err)
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return fmt.Errorf("secure file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close private file: %w", err)
	}
	return nil
}

// WritePrivateFile writes data to a 0600 file inside a 0700 parent directory.
func WritePrivateFile(path string, data []byte) error {
	if err := SecureDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := SecureFile(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open private file: %w", err)
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return fmt.Errorf("secure file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write private file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close private file: %w", err)
	}
	return nil
}

// PermissionIssue describes an entry whose mode is too broad or whose type
// cannot be repaired safely.
type PermissionIssue struct {
	Path        string
	Actual      fs.FileMode
	Expected    fs.FileMode
	Unsupported bool
}

var privateStoreFiles = []string{
	"config.json",
	"credentials.json",
	"xoxc_credentials.json",
	"sync_state.json",
	"slacli.db",
	"slacli.db-wal",
	"slacli.db-shm",
	"drafts.json",
}

// CheckPermissions reports permission issues under the store directory without
// changing any files.
func CheckPermissions(c *Config) ([]PermissionIssue, error) {
	root := filepath.Clean(c.StoreDir)
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect store dir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("store path is not a directory: %s", root)
	}

	issues := []PermissionIssue{}
	if info.Mode().Perm() != 0700 {
		issues = append(issues, PermissionIssue{Path: root, Actual: info.Mode().Perm(), Expected: 0700})
	}
	for _, name := range privateStoreFiles {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return issues, fmt.Errorf("inspect store file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			issues = append(issues, PermissionIssue{Path: path, Actual: info.Mode(), Unsupported: true})
			continue
		}
		if info.Mode().Perm() != 0600 {
			issues = append(issues, PermissionIssue{Path: path, Actual: info.Mode().Perm(), Expected: 0600})
		}
	}
	return issues, nil
}

// RepairPermissions applies 0700 to the store directory and 0600 to known
// private store files. It does not follow or modify symlinks.
func RepairPermissions(c *Config) error {
	if err := SecureDir(c.StoreDir); err != nil {
		return err
	}
	for _, name := range privateStoreFiles {
		if err := SecureFile(filepath.Join(c.StoreDir, name)); err != nil {
			return fmt.Errorf("repair permissions for %s: %w", name, err)
		}
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
	if err := WritePrivateFile(configPath, data); err != nil {
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

// XoxcCredentialsPath returns the path to xoxc credentials file
func (c *Config) XoxcCredentialsPath() string {
	return filepath.Join(c.StoreDir, "xoxc_credentials.json")
}

// AddWhitelistChannel adds a channel to the whitelist
func (c *Config) AddWhitelistChannel(channel string) bool {
	for _, ch := range c.WhitelistChannels {
		if ch == channel {
			return false // Already exists
		}
	}
	c.WhitelistChannels = append(c.WhitelistChannels, channel)
	return true
}

// RemoveWhitelistChannel removes a channel from the whitelist
func (c *Config) RemoveWhitelistChannel(channel string) bool {
	for i, ch := range c.WhitelistChannels {
		if ch == channel {
			c.WhitelistChannels = append(c.WhitelistChannels[:i], c.WhitelistChannels[i+1:]...)
			return true
		}
	}
	return false
}

// IsWhitelisted checks if a channel is in the whitelist
func (c *Config) IsWhitelisted(channelID, channelName string) bool {
	for _, ch := range c.WhitelistChannels {
		if ch == channelID || ch == channelName || ch == "#"+channelName {
			return true
		}
	}
	return false
}
