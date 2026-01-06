package sync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
	"slacli/internal/slack"
)

// Options for sync operation
type Options struct {
	Full         bool
	ChannelsOnly bool
	Days         int
	Follow       bool
}

// Syncer handles syncing data from Slack to local database
type Syncer struct {
	cfg    *config.Config
	client *http.Client
	api    *slack.API
}

// New creates a new Syncer
func New(cfg *config.Config, client *http.Client) *Syncer {
	return &Syncer{
		cfg:    cfg,
		client: client,
		api:    slack.NewAPI(client),
	}
}

// Run performs a sync operation
func (s *Syncer) Run(opts Options) (output.SyncResult, error) {
	start := time.Now()
	result := output.SyncResult{}

	// Open database
	store, err := db.Open(s.cfg)
	if err != nil {
		return result, fmt.Errorf("open database: %w", err)
	}
	defer store.Close()

	// Load or create sync state
	state, err := db.LoadSyncState(s.cfg)
	if err != nil {
		state = &db.SyncState{
			ChannelCursors: make(map[string]string),
			LastMessageTS:  make(map[string]string),
		}
	}

	if opts.Full {
		// Reset cursors for full sync
		state.ChannelCursors = make(map[string]string)
		state.LastMessageTS = make(map[string]string)
	}

	// Get auth info
	authInfo, err := s.api.GetAuthInfo()
	if err != nil {
		return result, fmt.Errorf("get auth info: %w", err)
	}
	state.UserID = authInfo.UserID
	state.TeamID = authInfo.TeamID

	// Sync users first (needed for email mapping)
	output.Info("Syncing users...")
	usersSynced, err := s.syncUsers(store)
	if err != nil {
		return result, fmt.Errorf("sync users: %w", err)
	}
	result.UsersSynced = usersSynced

	// Sync channels
	output.Info("Syncing channels...")
	channelsSynced, err := s.syncChannels(store)
	if err != nil {
		return result, fmt.Errorf("sync channels: %w", err)
	}
	result.ChannelsSynced = channelsSynced

	if opts.ChannelsOnly {
		state.LastSync = time.Now().Format(time.RFC3339)
		db.SaveSyncState(s.cfg, state)
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	// Calculate oldest timestamp for message sync
	oldest := time.Now().AddDate(0, 0, -opts.Days).Unix()
	oldestTS := fmt.Sprintf("%d.000000", oldest)

	// Sync messages for each channel
	output.Info("Syncing messages...")
	messagesSynced, err := s.syncMessages(store, state, oldestTS)
	if err != nil {
		return result, fmt.Errorf("sync messages: %w", err)
	}
	result.MessagesSynced = messagesSynced

	// Save sync state
	state.LastSync = time.Now().Format(time.RFC3339)
	if err := db.SaveSyncState(s.cfg, state); err != nil {
		return result, fmt.Errorf("save sync state: %w", err)
	}

	result.Duration = time.Since(start).Round(time.Second).String()
	return result, nil
}

// Follow runs continuous sync
func (s *Syncer) Follow(opts Options) error {
	for {
		result, err := s.Run(opts)
		if err != nil {
			output.Error("Sync error: %v", err)
		} else {
			output.Info("Synced %d channels, %d messages, %d users",
				result.ChannelsSynced, result.MessagesSynced, result.UsersSynced)
		}

		// Wait before next sync
		time.Sleep(30 * time.Second)
	}
}

func (s *Syncer) syncUsers(store *db.Store) (int, error) {
	count := 0
	cursor := ""

	for {
		resp, err := s.api.ListUsers(cursor)
		if err != nil {
			return count, err
		}

		for _, user := range resp.Members {
			if user.Deleted {
				continue
			}

			if err := s.upsertUser(store, &user); err != nil {
				output.Debug("Failed to insert user %s: %v", user.ID, err)
				continue
			}
			count++
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return count, nil
}

func (s *Syncer) upsertUser(store *db.Store, user *slack.UserInfo) error {
	query := `INSERT OR REPLACE INTO users (id, email, name, display_name, avatar_url, is_bot, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	name := user.RealName
	if name == "" {
		name = user.Name
	}

	_, err := store.DB().Exec(query,
		user.ID,
		user.Profile.Email,
		name,
		user.Profile.DisplayName,
		user.Profile.Image48,
		user.IsBot,
		time.Now().Format(time.RFC3339),
	)
	return err
}

func (s *Syncer) syncChannels(store *db.Store) (int, error) {
	count := 0
	cursor := ""

	for {
		resp, err := s.api.ListChannels(cursor)
		if err != nil {
			return count, err
		}

		for _, ch := range resp.Channels {
			if err := s.upsertChannel(store, &ch); err != nil {
				output.Debug("Failed to insert channel %s: %v", ch.ID, err)
				continue
			}
			count++
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return count, nil
}

func (s *Syncer) upsertChannel(store *db.Store, ch *slack.ChannelInfo) error {
	query := `INSERT OR REPLACE INTO channels (id, name, type, is_private, is_archived, unread_count, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	name := ch.Name
	if ch.IsIM && name == "" {
		// For DMs, try to get the other user's name
		name = ch.User
	}

	_, err := store.DB().Exec(query,
		ch.ID,
		name,
		ch.GetChannelType(),
		ch.IsPrivate,
		ch.IsArchived,
		ch.UnreadCount,
		time.Now().Format(time.RFC3339),
	)
	return err
}

func (s *Syncer) syncMessages(store *db.Store, state *db.SyncState, oldestTS string) (int, error) {
	count := 0

	// Get all channels
	channels, err := store.ListChannels(db.ChannelListOptions{Limit: 1000})
	if err != nil {
		return count, err
	}

	for _, ch := range channels {
		n, err := s.syncChannelMessages(store, state, ch.ID, oldestTS)
		if err != nil {
			output.Debug("Failed to sync messages for %s: %v", ch.ID, err)
			continue
		}
		count += n

		if n > 0 {
			output.Debug("Synced %d messages from %s", n, ch.Name)
		}
	}

	return count, nil
}

func (s *Syncer) syncChannelMessages(store *db.Store, state *db.SyncState, channelID, oldestTS string) (int, error) {
	count := 0
	cursor := state.ChannelCursors[channelID]

	// Get the last message timestamp we have for this channel
	lastTS := state.LastMessageTS[channelID]
	if lastTS == "" {
		lastTS = oldestTS
	}

	for {
		resp, err := s.api.GetHistory(channelID, cursor, 200, lastTS, "")
		if err != nil {
			return count, err
		}

		for _, msg := range resp.Messages {
			if err := s.upsertMessage(store, channelID, &msg); err != nil {
				output.Debug("Failed to insert message: %v", err)
				continue
			}
			count++

			// Update last message timestamp
			if msg.TS > state.LastMessageTS[channelID] {
				state.LastMessageTS[channelID] = msg.TS
			}
		}

		// Update channel's last message timestamp
		if len(resp.Messages) > 0 {
			latestTS := resp.Messages[0].TS
			s.updateChannelLastMessage(store, channelID, latestTS)
		}

		cursor = resp.ResponseMetadata.NextCursor
		state.ChannelCursors[channelID] = cursor

		if !resp.HasMore || cursor == "" {
			break
		}
	}

	return count, nil
}

func (s *Syncer) upsertMessage(store *db.Store, channelID string, msg *slack.MessageInfo) error {
	// Skip non-message types
	if msg.Type != "message" {
		return nil
	}
	// Skip certain subtypes
	if msg.Subtype == "channel_join" || msg.Subtype == "channel_leave" {
		return nil
	}

	// Get user email
	var email, name string
	err := store.DB().QueryRow("SELECT email, name FROM users WHERE id = ?", msg.User).Scan(&email, &name)
	if err != nil {
		email = ""
		name = ""
	}

	// Convert reactions to JSON
	reactionsJSON := "[]"
	if len(msg.Reactions) > 0 {
		if data, err := json.Marshal(msg.Reactions); err == nil {
			reactionsJSON = string(data)
		}
	}

	// Convert Slack timestamp to RFC3339
	timestamp := slackTSToRFC3339(msg.TS)

	query := `INSERT OR REPLACE INTO messages
		(id, channel_id, author_id, author_email, author_name, text, timestamp, thread_ts, reply_count, reactions, edited)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = store.DB().Exec(query,
		msg.TS,
		channelID,
		msg.User,
		email,
		name,
		msg.Text,
		timestamp,
		msg.ThreadTS,
		msg.ReplyCount,
		reactionsJSON,
		msg.Edited != nil,
	)
	return err
}

func (s *Syncer) updateChannelLastMessage(store *db.Store, channelID, ts string) {
	timestamp := slackTSToRFC3339(ts)
	store.DB().Exec("UPDATE channels SET last_message_ts = ? WHERE id = ?", timestamp, channelID)
}

// slackTSToRFC3339 converts Slack timestamp (e.g., "1234567890.123456") to RFC3339
func slackTSToRFC3339(ts string) string {
	var secs, usecs int64
	fmt.Sscanf(ts, "%d.%d", &secs, &usecs)
	t := time.Unix(secs, usecs*1000)
	return t.Format(time.RFC3339)
}
