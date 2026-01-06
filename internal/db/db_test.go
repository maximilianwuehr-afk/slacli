//go:build cgo

package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"slacli/internal/config"
)

func setupTestDB(t *testing.T) (*Store, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "slacli-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	cfg := &config.Config{
		StoreDir: tmpDir,
	}

	store, err := Open(cfg)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func TestOpen_CreatesDatabase(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Verify database file exists
	info, err := os.Stat(filepath.Join(store.cfg.StoreDir, "slacli.db"))
	if err != nil {
		t.Fatalf("Database file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("Database file is empty")
	}
}

func TestMigrate_CreatesTables(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Check that tables exist
	tables := []string{"channels", "users", "messages", "sync_state"}
	for _, table := range tables {
		var name string
		err := store.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("Table %s not found: %v", table, err)
		}
	}

	// Check FTS table
	var name string
	err := store.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='messages_fts'").Scan(&name)
	if err != nil {
		t.Errorf("FTS table not found: %v", err)
	}
}

func TestListChannels_Empty(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	channels, err := store.ListChannels(ChannelListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListChannels failed: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("Expected 0 channels, got %d", len(channels))
	}
}

func TestListChannels_WithData(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert test channels
	_, err := store.db.Exec(`
		INSERT INTO channels (id, name, type, is_private, last_message_ts)
		VALUES
			('C1', 'general', 'channel', 0, '2025-01-06T10:00:00Z'),
			('C2', 'random', 'channel', 0, '2025-01-06T11:00:00Z'),
			('D1', 'dm-user', 'dm', 1, '2025-01-06T09:00:00Z')
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Test default sort (last_received)
	channels, err := store.ListChannels(ChannelListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListChannels failed: %v", err)
	}
	if len(channels) != 3 {
		t.Errorf("Expected 3 channels, got %d", len(channels))
	}
	if channels[0].Name != "random" {
		t.Errorf("Expected 'random' first (most recent), got %s", channels[0].Name)
	}

	// Test type filter
	channels, err = store.ListChannels(ChannelListOptions{Type: "dm", Limit: 10})
	if err != nil {
		t.Fatalf("ListChannels with type filter failed: %v", err)
	}
	if len(channels) != 1 {
		t.Errorf("Expected 1 DM, got %d", len(channels))
	}

	// Test sort by name
	channels, err = store.ListChannels(ChannelListOptions{SortBy: "name", Limit: 10})
	if err != nil {
		t.Fatalf("ListChannels with sort failed: %v", err)
	}
	if channels[0].Name != "dm-user" {
		t.Errorf("Expected 'dm-user' first (alphabetically), got %s", channels[0].Name)
	}
}

func TestSearchMessages_FTS(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert test channel
	_, err := store.db.Exec(`INSERT INTO channels (id, name, type) VALUES ('C1', 'general', 'channel')`)
	if err != nil {
		t.Fatalf("Failed to insert channel: %v", err)
	}

	// Insert test messages
	_, err = store.db.Exec(`
		INSERT INTO messages (id, channel_id, author_email, author_name, text, timestamp)
		VALUES
			('1', 'C1', 'alice@test.com', 'Alice', 'Hello world', '2025-01-06T10:00:00Z'),
			('2', 'C1', 'bob@test.com', 'Bob', 'Deployment is complete', '2025-01-06T10:01:00Z'),
			('3', 'C1', 'alice@test.com', 'Alice', 'Great job on the deployment!', '2025-01-06T10:02:00Z')
	`)
	if err != nil {
		t.Fatalf("Failed to insert messages: %v", err)
	}

	// Search for "deployment"
	messages, err := store.SearchMessages(SearchOptions{Query: "deployment", Limit: 10})
	if err != nil {
		t.Fatalf("SearchMessages failed: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages with 'deployment', got %d", len(messages))
	}

	// Search with author filter
	messages, err = store.SearchMessages(SearchOptions{Query: "deployment", From: "bob@test.com", Limit: 10})
	if err != nil {
		t.Fatalf("SearchMessages with filter failed: %v", err)
	}
	if len(messages) != 1 {
		t.Errorf("Expected 1 message from bob, got %d", len(messages))
	}
}

func TestListMessages(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert test data
	_, err := store.db.Exec(`INSERT INTO channels (id, name, type) VALUES ('C1', 'general', 'channel')`)
	if err != nil {
		t.Fatalf("Failed to insert channel: %v", err)
	}

	_, err = store.db.Exec(`
		INSERT INTO messages (id, channel_id, author_email, text, timestamp)
		VALUES
			('1', 'C1', 'user@test.com', 'Message 1', '2025-01-06T10:00:00Z'),
			('2', 'C1', 'user@test.com', 'Message 2', '2025-01-06T10:01:00Z'),
			('3', 'C1', 'user@test.com', 'Message 3', '2025-01-06T10:02:00Z')
	`)
	if err != nil {
		t.Fatalf("Failed to insert messages: %v", err)
	}

	// List messages
	messages, err := store.ListMessages(MessageListOptions{Channel: "C1", Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(messages))
	}

	// Newest first
	if messages[0].ID != "3" {
		t.Errorf("Expected newest message first, got %s", messages[0].ID)
	}

	// Test limit
	messages, err = store.ListMessages(MessageListOptions{Channel: "C1", Limit: 2})
	if err != nil {
		t.Fatalf("ListMessages with limit failed: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestStats(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert test data
	_, err := store.db.Exec(`
		INSERT INTO channels (id, name, type) VALUES ('C1', 'general', 'channel');
		INSERT INTO users (id, email, name) VALUES ('U1', 'user@test.com', 'User');
		INSERT INTO messages (id, channel_id, text, timestamp) VALUES
			('1', 'C1', 'Hello', '2025-01-01T10:00:00Z'),
			('2', 'C1', 'World', '2025-01-06T10:00:00Z');
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.ChannelCount != 1 {
		t.Errorf("Expected 1 channel, got %d", stats.ChannelCount)
	}
	if stats.UserCount != 1 {
		t.Errorf("Expected 1 user, got %d", stats.UserCount)
	}
	if stats.MessageCount != 2 {
		t.Errorf("Expected 2 messages, got %d", stats.MessageCount)
	}
	if stats.SizeBytes <= 0 {
		t.Error("Expected positive size")
	}
}

func TestPrune(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert old and new messages
	oldDate := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	newDate := time.Now().Format(time.RFC3339)

	_, err := store.db.Exec(`INSERT INTO channels (id, name, type) VALUES ('C1', 'general', 'channel')`)
	if err != nil {
		t.Fatalf("Failed to insert channel: %v", err)
	}

	_, err = store.db.Exec(`
		INSERT INTO messages (id, channel_id, text, timestamp) VALUES
			('1', 'C1', 'Old message', ?),
			('2', 'C1', 'New message', ?)
	`, oldDate, newDate)
	if err != nil {
		t.Fatalf("Failed to insert messages: %v", err)
	}

	// Preview
	preview, err := store.PrunePreview(PruneOptions{OlderThanDays: 90})
	if err != nil {
		t.Fatalf("PrunePreview failed: %v", err)
	}
	if preview.DeletedCount != 1 {
		t.Errorf("Expected 1 message to prune, got %d", preview.DeletedCount)
	}

	// Actual prune
	result, err := store.Prune(PruneOptions{OlderThanDays: 90})
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if result.DeletedCount != 1 {
		t.Errorf("Expected 1 message pruned, got %d", result.DeletedCount)
	}

	// Verify
	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if count != 1 {
		t.Errorf("Expected 1 message remaining, got %d", count)
	}
}

func TestVacuum(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Just verify it doesn't error
	err := store.Vacuum()
	if err != nil {
		t.Errorf("Vacuum failed: %v", err)
	}
}

func TestResolveChannel(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert test channel
	_, err := store.db.Exec(`INSERT INTO channels (id, name, type) VALUES ('C123', 'general', 'channel')`)
	if err != nil {
		t.Fatalf("Failed to insert channel: %v", err)
	}

	// Resolve by ID
	id, err := store.resolveChannel("C123")
	if err != nil {
		t.Errorf("resolveChannel by ID failed: %v", err)
	}
	if id != "C123" {
		t.Errorf("Expected C123, got %s", id)
	}

	// Resolve by name
	id, err = store.resolveChannel("general")
	if err != nil {
		t.Errorf("resolveChannel by name failed: %v", err)
	}
	if id != "C123" {
		t.Errorf("Expected C123, got %s", id)
	}

	// Resolve with # prefix
	id, err = store.resolveChannel("#general")
	if err != nil {
		t.Errorf("resolveChannel with # failed: %v", err)
	}
	if id != "C123" {
		t.Errorf("Expected C123, got %s", id)
	}

	// Non-existent channel
	_, err = store.resolveChannel("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent channel")
	}
}

func TestSyncState(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Set value
	err := store.SetSyncStateValue("test_key", "test_value")
	if err != nil {
		t.Fatalf("SetSyncStateValue failed: %v", err)
	}

	// Get value
	value, err := store.GetSyncStateValue("test_key")
	if err != nil {
		t.Fatalf("GetSyncStateValue failed: %v", err)
	}
	if value != "test_value" {
		t.Errorf("Expected 'test_value', got '%s'", value)
	}

	// Update value
	err = store.SetSyncStateValue("test_key", "new_value")
	if err != nil {
		t.Fatalf("SetSyncStateValue update failed: %v", err)
	}

	value, err = store.GetSyncStateValue("test_key")
	if err != nil {
		t.Fatalf("GetSyncStateValue after update failed: %v", err)
	}
	if value != "new_value" {
		t.Errorf("Expected 'new_value', got '%s'", value)
	}
}
