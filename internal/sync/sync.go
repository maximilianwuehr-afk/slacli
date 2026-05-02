package sync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
	"slacli/internal/slack"
)

const (
	// Number of concurrent workers for parallel fetching
	// Slack allows bursts, backs off on 429
	workerPoolSize = 100
	// Cache duration for channel IDs from search
	channelCacheDuration = 3 * time.Hour
)

// Options for sync operation
type Options struct {
	Full         bool
	ChannelsOnly bool
	Days         int
	ActiveDays   int  // Only sync channels with activity in last N days (0=all)
	MyChannels   bool // Only sync channels where user has posted
	UnreadOnly   bool // Only sync channels with unread messages
	Follow       bool
	Threads      bool   // Resync thread replies for existing messages
	Channel      string // Only sync specific channel
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
	defer func() { _ = store.Close() }()

	// Thread-only resync mode
	if opts.Threads {
		return s.runThreadResync(store, opts, start)
	}

	// Load or create sync state
	state, err := db.LoadSyncState(s.cfg)
	if err != nil {
		state = &db.SyncState{
			ChannelCursors:  make(map[string]string),
			LastMessageTS:   make(map[string]string),
			ChannelLatestTS: make(map[string]string),
		}
	}
	// Ensure maps are initialized
	if state.ChannelCursors == nil {
		state.ChannelCursors = make(map[string]string)
	}
	if state.LastMessageTS == nil {
		state.LastMessageTS = make(map[string]string)
	}
	if state.ChannelLatestTS == nil {
		state.ChannelLatestTS = make(map[string]string)
	}
	if state.ChannelLastSynced == nil {
		state.ChannelLastSynced = make(map[string]string)
	}

	if opts.Full {
		// Reset cursors for full sync
		state.ChannelCursors = make(map[string]string)
		state.LastMessageTS = make(map[string]string)
		state.ChannelLatestTS = make(map[string]string)
		state.ChannelLastSynced = make(map[string]string)
		state.CachedChannelIDs = nil
		state.CachedChannelsTime = ""
	}

	// Get auth info
	authInfo, err := s.api.GetAuthInfo()
	if err != nil {
		return result, fmt.Errorf("get auth info: %w", err)
	}
	state.UserID = authInfo.UserID
	state.TeamID = authInfo.TeamID

	// Fast path: --my-channels skips full sync, uses search to find channels
	if opts.MyChannels {
		return s.runMyChannelsSync(store, state, opts, start)
	}

	// Unread-only sync: only sync channels with unread messages
	if opts.UnreadOnly {
		return s.runUnreadSync(store, state, opts, start)
	}

	// Full sync path: sync all users and channels
	output.Info("Syncing users...")
	usersSynced, err := s.syncUsers(store)
	if err != nil {
		return result, fmt.Errorf("sync users: %w", err)
	}
	result.UsersSynced = usersSynced

	output.Info("Syncing channels...")
	channelsSynced, err := s.syncChannels(store)
	if err != nil {
		return result, fmt.Errorf("sync channels: %w", err)
	}
	result.ChannelsSynced = channelsSynced

	if opts.ChannelsOnly {
		state.LastSync = time.Now().Format(time.RFC3339)
		if err := db.SaveSyncState(s.cfg, state); err != nil {
			return result, fmt.Errorf("save sync state: %w", err)
		}
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	// Calculate oldest timestamp for message sync
	oldest := time.Now().AddDate(0, 0, -opts.Days).Unix()
	oldestTS := fmt.Sprintf("%d.000000", oldest)

	// Get channel filter set (whitelist only for full sync)
	var channelFilter map[string]bool

	// Add whitelisted channels
	if len(s.cfg.WhitelistChannels) > 0 {
		if channelFilter == nil {
			channelFilter = make(map[string]bool)
		}
		// Resolve whitelist channel names to IDs
		for _, ch := range s.cfg.WhitelistChannels {
			if id, err := s.api.ResolveChannel(ch); err == nil {
				channelFilter[id] = true
			}
		}
		output.Info("Added %d whitelisted channels", len(s.cfg.WhitelistChannels))
	}

	// Sync messages for each channel
	output.Info("Syncing messages...")
	messagesSynced, err := s.syncMessages(store, state, oldestTS, opts.ActiveDays, channelFilter)
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

// runMyChannelsSync is the fast path for --my-channels
// It skips full user/channel sync and only syncs channels the user is active in
func (s *Syncer) runMyChannelsSync(store *db.Store, state *db.SyncState, opts Options, start time.Time) (output.SyncResult, error) {
	result := output.SyncResult{}

	days := opts.Days
	if opts.ActiveDays > 0 {
		days = opts.ActiveDays
	}

	// Step 1: Get channel IDs (from cache or search)
	myChannelIDs, fromCache := s.getCachedOrSearchChannels(state, days)
	if !fromCache {
		output.Info("Finding your channels via search...")
		var err error
		myChannelIDs, err = s.api.GetMyChannelIDs(days)
		if err != nil {
			return result, fmt.Errorf("search channels: %w", err)
		}
		// Cache the results
		state.CachedChannelIDs = myChannelIDs
		state.CachedChannelsTime = time.Now().Format(time.RFC3339)
	} else {
		output.Info("Using cached channel list (%d channels)", len(myChannelIDs))
	}

	// Add whitelisted channels
	whitelistIDs := []string{}
	for _, ch := range s.cfg.WhitelistChannels {
		if id, err := s.api.ResolveChannel(ch); err == nil {
			whitelistIDs = append(whitelistIDs, id)
		}
	}

	// Combine and dedupe
	channelSet := make(map[string]bool)
	for _, id := range myChannelIDs {
		channelSet[id] = true
	}
	for _, id := range whitelistIDs {
		channelSet[id] = true
	}

	output.Info("Found %d channels (search: %d, whitelist: %d)", len(channelSet), len(myChannelIDs), len(whitelistIDs))

	if len(channelSet) == 0 {
		output.Info("No channels to sync")
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	// Convert to slice for parallel processing
	channelIDs := make([]string, 0, len(channelSet))
	for id := range channelSet {
		channelIDs = append(channelIDs, id)
	}

	// Step 2: Fetch channel info in parallel and check for changes
	output.Info("Fetching channel details...")
	channelsToSync := s.fetchChannelInfoParallel(store, state, channelIDs)
	result.ChannelsSynced = len(channelIDs)

	if len(channelsToSync) == 0 {
		output.Info("All channels up to date")
		state.LastSync = time.Now().Format(time.RFC3339)
		if err := db.SaveSyncState(s.cfg, state); err != nil {
			return result, fmt.Errorf("save sync state: %w", err)
		}
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	// Step 3: Sync messages for changed channels in parallel
	output.Info("Syncing messages for %d channels...", len(channelsToSync))
	oldest := time.Now().AddDate(0, 0, -opts.Days).Unix()
	oldestTS := fmt.Sprintf("%d.000000", oldest)

	messagesSynced := s.syncMessagesParallel(store, state, channelsToSync, oldestTS)
	result.MessagesSynced = messagesSynced

	// Save state
	state.LastSync = time.Now().Format(time.RFC3339)
	if err := db.SaveSyncState(s.cfg, state); err != nil {
		return result, fmt.Errorf("save sync state: %w", err)
	}

	result.Duration = time.Since(start).Round(time.Second).String()
	return result, nil
}

// runUnreadSync syncs only channels with unread messages
func (s *Syncer) runUnreadSync(store *db.Store, state *db.SyncState, opts Options, start time.Time) (output.SyncResult, error) {
	result := output.SyncResult{}

	output.Info("Finding channels with unread messages...")

	// Get channels with unread messages from API
	unreadChannels, err := s.api.GetChannelsWithUnread()
	if err != nil {
		return result, fmt.Errorf("get unread channels: %w", err)
	}

	if len(unreadChannels) == 0 {
		output.Info("No unread channels found")
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	output.Info("Found %d channels with unread messages", len(unreadChannels))

	// Prepare channel info for sync (bypass skip-recently-synced for unread)
	var channelsToSync []channelSyncInfo
	for _, ch := range unreadChannels {
		// Upsert channel to DB with last_read
		if err := s.upsertChannelWithLastRead(store, &ch); err != nil {
			output.Debug("Failed to insert channel %s: %v", ch.ID, err)
			continue
		}

		channelsToSync = append(channelsToSync, channelSyncInfo{
			ID:       ch.ID,
			LatestTS: ch.Latest.TS,
		})
	}

	result.ChannelsSynced = len(channelsToSync)

	if len(channelsToSync) == 0 {
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	// Sync messages for unread channels
	output.Info("Syncing messages for %d unread channels...", len(channelsToSync))
	oldest := time.Now().AddDate(0, 0, -opts.Days).Unix()
	oldestTS := fmt.Sprintf("%d.000000", oldest)

	messagesSynced := s.syncMessagesParallel(store, state, channelsToSync, oldestTS)
	result.MessagesSynced = messagesSynced

	// Save state
	state.LastSync = time.Now().Format(time.RFC3339)
	if err := db.SaveSyncState(s.cfg, state); err != nil {
		return result, fmt.Errorf("save sync state: %w", err)
	}

	result.Duration = time.Since(start).Round(time.Second).String()
	return result, nil
}

// runThreadResync resyncs thread replies for existing messages with reply_count > 0
func (s *Syncer) runThreadResync(store *db.Store, opts Options, start time.Time) (output.SyncResult, error) {
	result := output.SyncResult{}

	output.Info("Finding messages with thread replies to sync...")

	// Build query to find messages with replies
	query := `SELECT id, channel_id, reply_count FROM messages WHERE reply_count > 0 AND (thread_ts IS NULL OR thread_ts = '' OR thread_ts = id)`
	args := []interface{}{}

	// Filter by channel if specified
	if opts.Channel != "" {
		// Resolve channel name to ID if needed
		channelID := opts.Channel
		if resolved, err := s.api.ResolveChannel(opts.Channel); err == nil {
			channelID = resolved
		}
		query = `SELECT id, channel_id, reply_count FROM messages WHERE reply_count > 0 AND (thread_ts IS NULL OR thread_ts = '' OR thread_ts = id) AND channel_id = ?`
		args = append(args, channelID)
	}

	rows, err := store.DB().Query(query, args...)
	if err != nil {
		return result, fmt.Errorf("query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type threadInfo struct {
		threadTS   string
		channelID  string
		replyCount int
	}
	var threads []threadInfo

	for rows.Next() {
		var ti threadInfo
		if err := rows.Scan(&ti.threadTS, &ti.channelID, &ti.replyCount); err != nil {
			continue
		}
		threads = append(threads, ti)
	}

	if len(threads) == 0 {
		output.Info("No threads to sync")
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	output.Info("Found %d threads to sync", len(threads))

	// Sync threads in parallel
	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		totalSynced int
		sem         = make(chan struct{}, workerPoolSize)
	)

	for _, ti := range threads {
		wg.Add(1)
		sem <- struct{}{}

		go func(t threadInfo) {
			defer wg.Done()
			defer func() { <-sem }()

			n, err := s.syncThreadReplies(store, t.channelID, t.threadTS)
			if err != nil {
				output.Debug("Failed to sync thread %s: %v", t.threadTS, err)
				return
			}

			if n > 0 {
				output.Debug("Synced %d replies for thread %s", n, t.threadTS)
			}

			mu.Lock()
			totalSynced += n
			mu.Unlock()
		}(ti)
	}

	wg.Wait()

	result.MessagesSynced = totalSynced
	result.ChannelsSynced = len(threads) // repurpose as thread count
	result.Duration = time.Since(start).Round(time.Second).String()
	return result, nil
}

// getCachedOrSearchChannels returns cached channel IDs if fresh, or indicates search is needed
func (s *Syncer) getCachedOrSearchChannels(state *db.SyncState, days int) ([]string, bool) {
	if len(state.CachedChannelIDs) == 0 || state.CachedChannelsTime == "" {
		return nil, false
	}

	cachedTime, err := time.Parse(time.RFC3339, state.CachedChannelsTime)
	if err != nil {
		return nil, false
	}

	if time.Since(cachedTime) > channelCacheDuration {
		return nil, false
	}

	return state.CachedChannelIDs, true
}

// channelSyncInfo holds info needed to sync a channel
type channelSyncInfo struct {
	ID       string
	LatestTS string
}

// fetchChannelInfoParallel fetches channel info in parallel and returns channels that need syncing
// Sync criteria: latest.ts > our_last_sync (new messages exist) OR never synced
func (s *Syncer) fetchChannelInfoParallel(store *db.Store, state *db.SyncState, channelIDs []string) []channelSyncInfo {
	var (
		mu             sync.Mutex
		wg             sync.WaitGroup
		channelsToSync []channelSyncInfo
		skipped        int
		sem            = make(chan struct{}, workerPoolSize)
	)

	for _, channelID := range channelIDs {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(chID string) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			ch, err := s.api.GetChannelInfo(chID)
			if err != nil {
				output.Debug("Failed to get channel %s: %v", chID, err)
				return
			}

			// Upsert channel to DB (includes last_read, unread_count)
			if err := s.upsertChannel(store, ch); err != nil {
				output.Debug("Failed to insert channel %s: %v", chID, err)
				return
			}

			lastSynced := state.ChannelLastSynced[chID]

			// Never synced before - sync it
			if lastSynced == "" {
				mu.Lock()
				channelsToSync = append(channelsToSync, channelSyncInfo{
					ID:       chID,
					LatestTS: ch.Latest.TS,
				})
				mu.Unlock()
				return
			}

			// Sync if latest.ts > our last sync (new messages exist)
			if ch.Latest.TS != "" {
				latestRFC := slackTSToRFC3339(ch.Latest.TS)
				if latestRFC > lastSynced {
					mu.Lock()
					channelsToSync = append(channelsToSync, channelSyncInfo{
						ID:       chID,
						LatestTS: ch.Latest.TS,
					})
					mu.Unlock()
					return
				}
			}

			// No new messages, skip
			mu.Lock()
			skipped++
			mu.Unlock()
		}(channelID)
	}

	wg.Wait()

	if skipped > 0 {
		output.Info("Skipped %d channels (no new messages)", skipped)
	}
	return channelsToSync
}

// syncMessagesParallel syncs messages for multiple channels in parallel
func (s *Syncer) syncMessagesParallel(store *db.Store, state *db.SyncState, channels []channelSyncInfo, oldestTS string) int {
	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		totalSynced int
		sem         = make(chan struct{}, workerPoolSize)
		stateMu     sync.Mutex // Separate mutex for state updates
	)

	for _, ch := range channels {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(chInfo channelSyncInfo) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			n, err := s.syncChannelMessagesWithLock(store, state, &stateMu, chInfo.ID, oldestTS)
			if err != nil {
				output.Debug("Failed to sync messages for %s: %v", chInfo.ID, err)
				return
			}

			if n > 0 {
				output.Debug("Synced %d messages from channel", n)
			}

			mu.Lock()
			totalSynced += n
			mu.Unlock()

			// Update latest timestamp for skip-unchanged optimization
			if chInfo.LatestTS != "" {
				stateMu.Lock()
				state.ChannelLatestTS[chInfo.ID] = chInfo.LatestTS
				stateMu.Unlock()
			}

			// Track when this channel was synced
			stateMu.Lock()
			state.ChannelLastSynced[chInfo.ID] = time.Now().Format(time.RFC3339)
			stateMu.Unlock()
		}(ch)
	}

	wg.Wait()
	return totalSynced
}

// syncChannelMessagesWithLock syncs messages for a channel with mutex protection for state
func (s *Syncer) syncChannelMessagesWithLock(store *db.Store, state *db.SyncState, stateMu *sync.Mutex, channelID, oldestTS string) (int, error) {
	count := 0

	stateMu.Lock()
	cursor := state.ChannelCursors[channelID]
	lastTS := state.LastMessageTS[channelID]
	stateMu.Unlock()

	if lastTS == "" {
		lastTS = oldestTS
	}

	// Collect messages with threads to sync replies later
	var threadsToSync []string

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

			// Track threads that need reply syncing
			if msg.ReplyCount > 0 && msg.ThreadTS == "" {
				// This is a parent message with replies
				threadsToSync = append(threadsToSync, msg.TS)
			}

			// Update last message timestamp
			stateMu.Lock()
			if msg.TS > state.LastMessageTS[channelID] {
				state.LastMessageTS[channelID] = msg.TS
			}
			stateMu.Unlock()
		}

		// Update channel's last message timestamp
		if len(resp.Messages) > 0 {
			latestTS := resp.Messages[0].TS
			s.updateChannelLastMessage(store, channelID, latestTS)
		}

		cursor = resp.ResponseMetadata.NextCursor

		stateMu.Lock()
		state.ChannelCursors[channelID] = cursor
		stateMu.Unlock()

		if !resp.HasMore || cursor == "" {
			break
		}
	}

	// Sync thread replies for messages with reply_count > 0
	for _, threadTS := range threadsToSync {
		repliesSynced, err := s.syncThreadReplies(store, channelID, threadTS)
		if err != nil {
			output.Debug("Failed to sync thread %s: %v", threadTS, err)
			continue
		}
		count += repliesSynced
	}

	return count, nil
}

// syncThreadReplies fetches all replies for a thread
func (s *Syncer) syncThreadReplies(store *db.Store, channelID, threadTS string) (int, error) {
	count := 0
	cursor := ""

	for {
		resp, err := s.api.GetReplies(channelID, threadTS, cursor, 200)
		if err != nil {
			return count, err
		}

		for _, msg := range resp.Messages {
			// Skip the parent message (first message in replies is the parent)
			if msg.TS == threadTS {
				continue
			}

			if err := s.upsertMessage(store, channelID, &msg); err != nil {
				output.Debug("Failed to insert thread reply: %v", err)
				continue
			}
			count++
		}

		cursor = resp.ResponseMetadata.NextCursor
		if !resp.HasMore || cursor == "" {
			break
		}
	}

	return count, nil
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
	query := `INSERT OR REPLACE INTO channels (id, name, type, is_private, is_archived, unread_count, last_read, last_activity, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	name := ch.Name
	if ch.IsIM && name == "" {
		// For DMs, try to get the other user's name
		name = ch.User
	}

	// Convert Slack updated timestamp to RFC3339
	var lastActivity string
	if ch.Updated > 0 {
		lastActivity = time.Unix(int64(ch.Updated), 0).Format(time.RFC3339)
	}

	// Convert last_read timestamp if present
	var lastRead string
	if ch.LastRead != "" {
		lastRead = slackTSToRFC3339(ch.LastRead)
	}

	_, err := store.DB().Exec(query,
		ch.ID,
		name,
		ch.GetChannelType(),
		ch.IsPrivate,
		ch.IsArchived,
		ch.UnreadCount,
		lastRead,
		lastActivity,
		time.Now().Format(time.RFC3339),
	)
	return err
}

func (s *Syncer) upsertChannelWithLastRead(store *db.Store, ch *slack.ChannelInfo) error {
	return s.upsertChannel(store, ch)
}

func (s *Syncer) syncMessages(store *db.Store, state *db.SyncState, oldestTS string, activeDays int, channelFilter map[string]bool) (int, error) {
	count := 0

	// Get all channels
	channels, err := store.ListChannels(db.ChannelListOptions{Limit: 100000})
	if err != nil {
		return count, err
	}

	// Calculate cutoff for active channels filter
	var activeCutoff time.Time
	if activeDays > 0 {
		activeCutoff = time.Now().AddDate(0, 0, -activeDays)
	}

	activeCount := 0
	skippedCount := 0

	for _, ch := range channels {
		// Filter by channel filter set (--my-channels + whitelist)
		if channelFilter != nil && !channelFilter[ch.ID] {
			skippedCount++
			continue
		}

		// Filter by active days if specified (and no channel filter)
		if channelFilter == nil && activeDays > 0 && ch.LastActivity != "" {
			lastActivity, err := time.Parse(time.RFC3339, ch.LastActivity)
			if err == nil && lastActivity.Before(activeCutoff) {
				skippedCount++
				continue
			}
		}
		activeCount++

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

	if channelFilter != nil || activeDays > 0 {
		output.Info("Syncing %d channels (skipped %d)", activeCount, skippedCount)
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

	// Collect messages with threads to sync replies later
	var threadsToSync []string

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

			// Track threads that need reply syncing
			if msg.ReplyCount > 0 && msg.ThreadTS == "" {
				// This is a parent message with replies
				threadsToSync = append(threadsToSync, msg.TS)
			}

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

	// Sync thread replies for messages with reply_count > 0
	for _, threadTS := range threadsToSync {
		repliesSynced, err := s.syncThreadReplies(store, channelID, threadTS)
		if err != nil {
			output.Debug("Failed to sync thread %s: %v", threadTS, err)
			continue
		}
		count += repliesSynced
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
	if _, err := store.DB().Exec("UPDATE channels SET last_message_ts = ? WHERE id = ?", timestamp, channelID); err != nil {
		output.Debug("Failed to update channel %s last message timestamp: %v", channelID, err)
	}
}

// slackTSToRFC3339 converts Slack timestamp (e.g., "1234567890.123456") to RFC3339
func slackTSToRFC3339(ts string) string {
	var secs, usecs int64
	if _, err := fmt.Sscanf(ts, "%d.%d", &secs, &usecs); err != nil {
		return ts
	}
	t := time.Unix(secs, usecs*1000)
	return t.Format(time.RFC3339)
}
