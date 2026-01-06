package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"slacli/internal/config"
	"slacli/internal/output"
)

// Store represents the SQLite database
type Store struct {
	db  *sql.DB
	cfg *config.Config
}

// Open opens or creates the database
func Open(cfg *config.Config) (*Store, error) {
	dbPath := cfg.DatabasePath()

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := &Store{db: db, cfg: cfg}

	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return store, nil
}

// Close closes the database
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sql.DB for direct access
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate() error {
	schema := `
	-- Channels table
	CREATE TABLE IF NOT EXISTS channels (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		is_private INTEGER DEFAULT 0,
		is_archived INTEGER DEFAULT 0,
		last_message_ts TEXT,
		last_sent_ts TEXT,
		last_mention_ts TEXT,
		unread_count INTEGER DEFAULT 0,
		members TEXT,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT DEFAULT CURRENT_TIMESTAMP
	);

	-- Users table
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE,
		name TEXT NOT NULL,
		display_name TEXT,
		avatar_url TEXT,
		is_bot INTEGER DEFAULT 0,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

	-- Messages table
	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		channel_id TEXT NOT NULL,
		author_id TEXT,
		author_email TEXT,
		author_name TEXT,
		text TEXT,
		timestamp TEXT NOT NULL,
		thread_ts TEXT,
		reply_count INTEGER DEFAULT 0,
		reactions TEXT,
		edited INTEGER DEFAULT 0,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (channel_id) REFERENCES channels(id)
	);
	CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id);
	CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp);
	CREATE INDEX IF NOT EXISTS idx_messages_author ON messages(author_email);
	CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_ts);

	-- FTS virtual table for message search
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		id,
		channel_id,
		author_email,
		author_name,
		text,
		timestamp,
		content=messages,
		content_rowid=rowid
	);

	-- Triggers to keep FTS in sync
	CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, id, channel_id, author_email, author_name, text, timestamp)
		VALUES (new.rowid, new.id, new.channel_id, new.author_email, new.author_name, new.text, new.timestamp);
	END;

	CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, id, channel_id, author_email, author_name, text, timestamp)
		VALUES ('delete', old.rowid, old.id, old.channel_id, old.author_email, old.author_name, old.text, old.timestamp);
	END;

	CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, id, channel_id, author_email, author_name, text, timestamp)
		VALUES ('delete', old.rowid, old.id, old.channel_id, old.author_email, old.author_name, old.text, old.timestamp);
		INSERT INTO messages_fts(rowid, id, channel_id, author_email, author_name, text, timestamp)
		VALUES (new.rowid, new.id, new.channel_id, new.author_email, new.author_name, new.text, new.timestamp);
	END;

	-- Sync state table
	CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT DEFAULT CURRENT_TIMESTAMP
	);
	`

	_, err := s.db.Exec(schema)
	return err
}

// ChannelListOptions for listing channels
type ChannelListOptions struct {
	SortBy string
	Type   string
	Limit  int
	Unread bool
}

// ListChannels returns channels matching the options
func (s *Store) ListChannels(opts ChannelListOptions) ([]output.Channel, error) {
	query := `SELECT id, name, type, is_private, is_archived,
		COALESCE(last_message_ts, ''), COALESCE(last_sent_ts, ''),
		COALESCE(last_mention_ts, ''), unread_count, COALESCE(members, '[]')
		FROM channels WHERE 1=1`

	args := []interface{}{}

	if opts.Type != "" && opts.Type != "all" {
		query += " AND type = ?"
		args = append(args, opts.Type)
	}

	if opts.Unread {
		query += " AND unread_count > 0"
	}

	// Sort
	switch opts.SortBy {
	case "last_sent":
		query += " ORDER BY last_sent_ts DESC NULLS LAST"
	case "last_mention":
		query += " ORDER BY last_mention_ts DESC NULLS LAST"
	case "name":
		query += " ORDER BY name ASC"
	default: // last_received
		query += " ORDER BY last_message_ts DESC NULLS LAST"
	}

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []output.Channel
	for rows.Next() {
		var ch output.Channel
		var membersJSON string
		err := rows.Scan(&ch.ID, &ch.Name, &ch.Type, &ch.IsPrivate, &ch.IsArchived,
			&ch.LastMessageAt, &ch.LastSentAt, &ch.LastMentionAt, &ch.UnreadCount, &membersJSON)
		if err != nil {
			return nil, err
		}
		if membersJSON != "" {
			json.Unmarshal([]byte(membersJSON), &ch.Members)
		}
		channels = append(channels, ch)
	}

	return channels, rows.Err()
}

// MessageListOptions for listing messages
type MessageListOptions struct {
	Channel  string
	Limit    int
	Before   string
	After    string
	ThreadTS string
}

// ListMessages returns messages matching the options
func (s *Store) ListMessages(opts MessageListOptions) ([]output.Message, error) {
	// Resolve channel (could be name, ID, or email)
	channelID, err := s.resolveChannel(opts.Channel)
	if err != nil {
		return nil, err
	}

	query := `SELECT m.id, m.channel_id, COALESCE(c.name, ''), COALESCE(m.author_id, ''),
		COALESCE(m.author_email, ''), COALESCE(m.author_name, ''),
		COALESCE(m.text, ''), m.timestamp, COALESCE(m.thread_ts, ''),
		m.reply_count, COALESCE(m.reactions, '[]'), m.edited
		FROM messages m
		LEFT JOIN channels c ON m.channel_id = c.id
		WHERE m.channel_id = ?`

	args := []interface{}{channelID}

	if opts.ThreadTS != "" {
		query += " AND (m.thread_ts = ? OR m.id = ?)"
		args = append(args, opts.ThreadTS, opts.ThreadTS)
	}

	if opts.Before != "" {
		query += " AND m.timestamp < ?"
		args = append(args, opts.Before)
	}

	if opts.After != "" {
		query += " AND m.timestamp > ?"
		args = append(args, opts.After)
	}

	query += " ORDER BY m.timestamp DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	return s.queryMessages(query, args...)
}

// SearchOptions for searching messages
type SearchOptions struct {
	Query   string
	Channel string
	From    string
	After   string
	Before  string
	Limit   int
}

// SearchMessages performs full-text search
func (s *Store) SearchMessages(opts SearchOptions) ([]output.Message, error) {
	query := `SELECT m.id, m.channel_id, COALESCE(c.name, ''), COALESCE(m.author_id, ''),
		COALESCE(m.author_email, ''), COALESCE(m.author_name, ''),
		COALESCE(m.text, ''), m.timestamp, COALESCE(m.thread_ts, ''),
		m.reply_count, COALESCE(m.reactions, '[]'), m.edited
		FROM messages m
		LEFT JOIN channels c ON m.channel_id = c.id
		JOIN messages_fts fts ON m.rowid = fts.rowid
		WHERE messages_fts MATCH ?`

	// Escape the query for FTS5
	ftsQuery := escapeFTS(opts.Query)
	args := []interface{}{ftsQuery}

	if opts.Channel != "" {
		channelID, err := s.resolveChannel(opts.Channel)
		if err != nil {
			return nil, err
		}
		query += " AND m.channel_id = ?"
		args = append(args, channelID)
	}

	if opts.From != "" {
		query += " AND m.author_email = ?"
		args = append(args, opts.From)
	}

	if opts.After != "" {
		query += " AND m.timestamp >= ?"
		args = append(args, opts.After+"T00:00:00Z")
	}

	if opts.Before != "" {
		query += " AND m.timestamp <= ?"
		args = append(args, opts.Before+"T23:59:59Z")
	}

	query += " ORDER BY m.timestamp DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	return s.queryMessages(query, args...)
}

// MentionOptions for getting mentions
type MentionOptions struct {
	Channel string
	Limit   int
	Unread  bool
	Since   string
}

// GetMentions returns messages where user was mentioned
func (s *Store) GetMentions(opts MentionOptions) ([]output.Message, error) {
	// Get current user ID from sync state
	userID, err := s.GetSyncStateValue("user_id")
	if err != nil {
		return nil, fmt.Errorf("user not synced: %w", err)
	}

	query := `SELECT m.id, m.channel_id, COALESCE(c.name, ''), COALESCE(m.author_id, ''),
		COALESCE(m.author_email, ''), COALESCE(m.author_name, ''),
		COALESCE(m.text, ''), m.timestamp, COALESCE(m.thread_ts, ''),
		m.reply_count, COALESCE(m.reactions, '[]'), m.edited
		FROM messages m
		LEFT JOIN channels c ON m.channel_id = c.id
		WHERE m.text LIKE ?`

	// Match @mentions
	args := []interface{}{"%" + "<@" + userID + ">" + "%"}

	if opts.Channel != "" {
		channelID, err := s.resolveChannel(opts.Channel)
		if err != nil {
			return nil, err
		}
		query += " AND m.channel_id = ?"
		args = append(args, channelID)
	}

	if opts.Since != "" {
		query += " AND m.timestamp >= ?"
		args = append(args, opts.Since+"T00:00:00Z")
	}

	query += " ORDER BY m.timestamp DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	return s.queryMessages(query, args...)
}

// GetMessageContext returns messages around a specific message
func (s *Store) GetMessageContext(messageID string, before, after int) ([]output.Message, error) {
	// Get the target message to find its channel and timestamp
	var channelID, timestamp string
	err := s.db.QueryRow("SELECT channel_id, timestamp FROM messages WHERE id = ?", messageID).
		Scan(&channelID, &timestamp)
	if err != nil {
		return nil, fmt.Errorf("message not found: %w", err)
	}

	// Get messages before and after separately
	beforeQuery := `SELECT m.id, m.channel_id, COALESCE(c.name, ''), COALESCE(m.author_id, ''),
		COALESCE(m.author_email, ''), COALESCE(m.author_name, ''),
		COALESCE(m.text, ''), m.timestamp, COALESCE(m.thread_ts, ''),
		m.reply_count, COALESCE(m.reactions, '[]'), m.edited
		FROM messages m
		LEFT JOIN channels c ON m.channel_id = c.id
		WHERE m.channel_id = ? AND m.timestamp <= ?
		ORDER BY m.timestamp DESC LIMIT ?`

	afterQuery := `SELECT m.id, m.channel_id, COALESCE(c.name, ''), COALESCE(m.author_id, ''),
		COALESCE(m.author_email, ''), COALESCE(m.author_name, ''),
		COALESCE(m.text, ''), m.timestamp, COALESCE(m.thread_ts, ''),
		m.reply_count, COALESCE(m.reactions, '[]'), m.edited
		FROM messages m
		LEFT JOIN channels c ON m.channel_id = c.id
		WHERE m.channel_id = ? AND m.timestamp > ?
		ORDER BY m.timestamp ASC LIMIT ?`

	beforeMsgs, err := s.queryMessages(beforeQuery, channelID, timestamp, before+1)
	if err != nil {
		return nil, err
	}

	afterMsgs, err := s.queryMessages(afterQuery, channelID, timestamp, after)
	if err != nil {
		return nil, err
	}

	// Reverse beforeMsgs and combine
	result := make([]output.Message, 0, len(beforeMsgs)+len(afterMsgs))
	for i := len(beforeMsgs) - 1; i >= 0; i-- {
		result = append(result, beforeMsgs[i])
	}
	result = append(result, afterMsgs...)

	return result, nil
}

// UserListOptions for listing users
type UserListOptions struct {
	Search string
	Limit  int
}

// ListUsers returns users matching the options
func (s *Store) ListUsers(opts UserListOptions) ([]output.User, error) {
	query := `SELECT id, COALESCE(email, ''), name, COALESCE(display_name, ''),
		COALESCE(avatar_url, ''), is_bot FROM users WHERE 1=1`

	args := []interface{}{}

	if opts.Search != "" {
		query += " AND (name LIKE ? OR email LIKE ? OR display_name LIKE ?)"
		search := "%" + opts.Search + "%"
		args = append(args, search, search, search)
	}

	query += " ORDER BY name ASC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []output.User
	for rows.Next() {
		var u output.User
		err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.DisplayName, &u.AvatarURL, &u.IsBot)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	return users, rows.Err()
}

// Stats returns database statistics
func (s *Store) Stats() (output.DBStats, error) {
	var stats output.DBStats

	// Get file size
	size, err := s.Size()
	if err != nil {
		return stats, err
	}
	stats.SizeBytes = size

	// Count messages
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&stats.MessageCount)

	// Count channels
	s.db.QueryRow("SELECT COUNT(*) FROM channels").Scan(&stats.ChannelCount)

	// Count users
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&stats.UserCount)

	// Oldest/newest messages
	s.db.QueryRow("SELECT MIN(timestamp) FROM messages").Scan(&stats.OldestMessage)
	s.db.QueryRow("SELECT MAX(timestamp) FROM messages").Scan(&stats.NewestMessage)

	return stats, nil
}

// Size returns the database file size in bytes
func (s *Store) Size() (int64, error) {
	info, err := os.Stat(s.cfg.DatabasePath())
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// PruneOptions for pruning old messages
type PruneOptions struct {
	OlderThanDays int
	Channel       string
	DryRun        bool
}

// PrunePreview returns what would be deleted
func (s *Store) PrunePreview(opts PruneOptions) (output.PruneResult, error) {
	cutoff := time.Now().AddDate(0, 0, -opts.OlderThanDays).Format(time.RFC3339)

	query := "SELECT COUNT(*) FROM messages WHERE timestamp < ?"
	args := []interface{}{cutoff}

	if opts.Channel != "" {
		channelID, err := s.resolveChannel(opts.Channel)
		if err != nil {
			return output.PruneResult{}, err
		}
		query += " AND channel_id = ?"
		args = append(args, channelID)
	}

	var count int
	err := s.db.QueryRow(query, args...).Scan(&count)
	return output.PruneResult{DeletedCount: count}, err
}

// Prune deletes old messages
func (s *Store) Prune(opts PruneOptions) (output.PruneResult, error) {
	cutoff := time.Now().AddDate(0, 0, -opts.OlderThanDays).Format(time.RFC3339)

	query := "DELETE FROM messages WHERE timestamp < ?"
	args := []interface{}{cutoff}

	if opts.Channel != "" {
		channelID, err := s.resolveChannel(opts.Channel)
		if err != nil {
			return output.PruneResult{}, err
		}
		query += " AND channel_id = ?"
		args = append(args, channelID)
	}

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return output.PruneResult{}, err
	}

	count, _ := result.RowsAffected()
	return output.PruneResult{DeletedCount: int(count)}, nil
}

// Vacuum reclaims disk space
func (s *Store) Vacuum() error {
	_, err := s.db.Exec("VACUUM")
	return err
}

// Reset deletes all data
func Reset(cfg *config.Config, keepAuth bool) error {
	dbPath := cfg.DatabasePath()
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Also remove WAL and SHM files
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	if !keepAuth {
		os.Remove(cfg.CredentialsPath())
	}

	os.Remove(cfg.SyncStatePath())

	return nil
}

// Helper methods

func (s *Store) resolveChannel(channel string) (string, error) {
	// If it looks like an ID, use it directly
	if strings.HasPrefix(channel, "C") || strings.HasPrefix(channel, "D") || strings.HasPrefix(channel, "G") {
		return channel, nil
	}

	// Remove # prefix
	channel = strings.TrimPrefix(channel, "#")

	// Try to find by name
	var id string
	err := s.db.QueryRow("SELECT id FROM channels WHERE name = ?", channel).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Try to find DM by email
	if strings.Contains(channel, "@") {
		err = s.db.QueryRow(`SELECT c.id FROM channels c
			WHERE c.type = 'dm' AND c.members LIKE ?`, "%"+channel+"%").Scan(&id)
		if err == nil {
			return id, nil
		}
	}

	return "", fmt.Errorf("channel not found: %s", channel)
}

func (s *Store) queryMessages(query string, args ...interface{}) ([]output.Message, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []output.Message
	for rows.Next() {
		var msg output.Message
		var reactionsJSON string
		err := rows.Scan(&msg.ID, &msg.ChannelID, &msg.ChannelName, &msg.AuthorID,
			&msg.AuthorEmail, &msg.AuthorName, &msg.Text, &msg.Timestamp,
			&msg.ThreadTS, &msg.ReplyCount, &reactionsJSON, &msg.Edited)
		if err != nil {
			return nil, err
		}
		if reactionsJSON != "" && reactionsJSON != "[]" {
			json.Unmarshal([]byte(reactionsJSON), &msg.Reactions)
		}
		messages = append(messages, msg)
	}

	return messages, rows.Err()
}

func escapeFTS(query string) string {
	// Simple escaping for FTS5
	// Wrap each word in quotes to treat as literal
	words := strings.Fields(query)
	for i, w := range words {
		// Remove special FTS characters
		w = strings.ReplaceAll(w, "\"", "")
		w = strings.ReplaceAll(w, "*", "")
		words[i] = "\"" + w + "\""
	}
	return strings.Join(words, " ")
}

// Sync state helpers

// GetSyncStateValue returns a sync state value
func (s *Store) GetSyncStateValue(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM sync_state WHERE key = ?", key).Scan(&value)
	return value, err
}

// SetSyncStateValue sets a sync state value
func (s *Store) SetSyncStateValue(key, value string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO sync_state (key, value, updated_at)
		VALUES (?, ?, ?)`, key, value, time.Now().Format(time.RFC3339))
	return err
}

// SyncState represents sync state
type SyncState struct {
	LastSync        string            `json:"last_sync"`
	ChannelCursors  map[string]string `json:"channel_cursors"`
	LastMessageTS   map[string]string `json:"last_message_ts"`
	UserID          string            `json:"user_id"`
	TeamID          string            `json:"team_id"`
}

// LoadSyncState loads sync state from file
func LoadSyncState(cfg *config.Config) (*SyncState, error) {
	data, err := os.ReadFile(cfg.SyncStatePath())
	if err != nil {
		return nil, err
	}
	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// SaveSyncState saves sync state to file
func SaveSyncState(cfg *config.Config, state *SyncState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.SyncStatePath(), data, 0600)
}
