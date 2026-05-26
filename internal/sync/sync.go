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
	// Keep Slack API concurrency below bursty 429 territory. Higher fan-out
	// often makes sync slower because every worker recursively sleeps on 429s.
	channelInfoWorkerPoolSize = 32
	messageWorkerPoolSize     = 12
	threadWorkerPoolSize      = 8
	// Cache duration for channel IDs from search
	channelCacheDuration       = 3 * time.Hour
	activeChannelCacheDuration = 5 * time.Minute
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
	cfg       *config.Config
	client    *http.Client
	api       *slack.API
	userMu    sync.RWMutex
	userCache map[string]userIdentity
}

type userIdentity struct {
	email string
	name  string
}

// New creates a new Syncer
func New(cfg *config.Config, client *http.Client) *Syncer {
	return &Syncer{
		cfg:       cfg,
		client:    client,
		api:       slack.NewAPI(client),
		userCache: make(map[string]userIdentity),
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
		state.CachedActiveChannelIDs = nil
		state.CachedActiveChannelsTime = ""
		state.CachedActiveChannelsDays = 0
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

	// Fast active-days path: use Slack search to find recently active channels
	// instead of paginating and storing every channel in the workspace.
	if opts.ActiveDays > 0 && !opts.Full && !opts.ChannelsOnly {
		return s.runActiveDaysSync(store, state, opts, start)
	}

	return s.runFullSync(store, state, opts, start)
}

func (s *Syncer) runFullSync(store *db.Store, state *db.SyncState, opts Options, start time.Time) (output.SyncResult, error) {
	result := output.SyncResult{}

	progress := output.StartProgress("Syncing users", 0)
	usersSynced, err := s.syncUsers(store, progress)
	if err != nil {
		progress.Fail("failed")
		return result, fmt.Errorf("sync users: %w", err)
	}
	progress.Done(fmt.Sprintf("%d users", usersSynced))
	result.UsersSynced = usersSynced

	progress = output.StartProgress("Syncing channels", 0)
	channelsSynced, err := s.syncChannels(store, progress)
	if err != nil {
		progress.Fail("failed")
		return result, fmt.Errorf("sync channels: %w", err)
	}
	progress.Done(fmt.Sprintf("%d channels", channelsSynced))
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

// runActiveDaysSync is a fast path for --active-days. It finds recently active
// channels through Slack search, then only fetches details and histories there.
func (s *Syncer) runActiveDaysSync(store *db.Store, state *db.SyncState, opts Options, start time.Time) (output.SyncResult, error) {
	result := output.SyncResult{}

	activeChannelIDs, fromCache := s.getCachedActiveChannels(state, opts.ActiveDays)
	if fromCache {
		output.Info("Using cached active channel list (%d channels)", len(activeChannelIDs))
	} else {
		progress := output.StartProgress("Finding active channels via search", 0)
		var err error
		activeChannelIDs, err = s.api.GetActiveChannelIDs(opts.ActiveDays)
		if err != nil {
			progress.Fail("failed")
			output.Warn(fmt.Sprintf("Active channel search failed; falling back to full metadata sync: %v", err))
			return s.runFullSync(store, state, opts, start)
		}
		progress.Done(fmt.Sprintf("%d channels", len(activeChannelIDs)))
		state.CachedActiveChannelIDs = activeChannelIDs
		state.CachedActiveChannelsTime = time.Now().Format(time.RFC3339)
		state.CachedActiveChannelsDays = opts.ActiveDays
	}

	whitelistIDs := []string{}
	for _, ch := range s.cfg.WhitelistChannels {
		if id, err := s.api.ResolveChannel(ch); err == nil {
			whitelistIDs = append(whitelistIDs, id)
		}
	}

	channelSet := make(map[string]bool)
	for _, id := range activeChannelIDs {
		channelSet[id] = true
	}
	for _, id := range whitelistIDs {
		channelSet[id] = true
	}

	output.Info("Found %d active channels (search: %d, whitelist: %d)", len(channelSet), len(activeChannelIDs), len(whitelistIDs))
	if len(channelSet) == 0 {
		state.LastSync = time.Now().Format(time.RFC3339)
		if err := db.SaveSyncState(s.cfg, state); err != nil {
			return result, fmt.Errorf("save sync state: %w", err)
		}
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	channelIDs := make([]string, 0, len(channelSet))
	for id := range channelSet {
		channelIDs = append(channelIDs, id)
	}

	progress := output.StartProgress("Fetching active channel details", len(channelIDs))
	channelsToSync := s.fetchChannelInfoParallel(store, state, channelIDs, progress, fromCache)
	progress.Done(fmt.Sprintf("%d changed", len(channelsToSync)))
	result.ChannelsSynced = len(channelIDs)

	if len(channelsToSync) == 0 {
		output.Info("All active channels up to date")
		state.LastSync = time.Now().Format(time.RFC3339)
		if err := db.SaveSyncState(s.cfg, state); err != nil {
			return result, fmt.Errorf("save sync state: %w", err)
		}
		result.Duration = time.Since(start).Round(time.Second).String()
		return result, nil
	}

	oldest := time.Now().AddDate(0, 0, -opts.Days).Unix()
	oldestTS := fmt.Sprintf("%d.000000", oldest)

	output.Info("Skipping thread replies in active-days history sync; run `slacli sync --threads --active-days %d` to fill replies", opts.ActiveDays)
	progress = output.StartProgress("Syncing active channel histories", len(channelsToSync))
	messagesSynced := s.syncMessagesParallel(store, state, channelsToSync, oldestTS, progress, false)
	progress.Done(fmt.Sprintf("%d messages", messagesSynced))
	result.MessagesSynced = messagesSynced

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
		progress := output.StartProgress("Finding your channels via search", 0)
		var err error
		myChannelIDs, err = s.api.GetMyChannelIDs(days)
		if err != nil {
			progress.Fail("failed")
			return result, fmt.Errorf("search channels: %w", err)
		}
		progress.Done(fmt.Sprintf("%d channels", len(myChannelIDs)))
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
	progress := output.StartProgress("Fetching channel details", len(channelIDs))
	channelsToSync := s.fetchChannelInfoParallel(store, state, channelIDs, progress, false)
	progress.Done(fmt.Sprintf("%d changed", len(channelsToSync)))
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
	oldest := time.Now().AddDate(0, 0, -opts.Days).Unix()
	oldestTS := fmt.Sprintf("%d.000000", oldest)

	progress = output.StartProgress("Syncing channel histories", len(channelsToSync))
	messagesSynced := s.syncMessagesParallel(store, state, channelsToSync, oldestTS, progress, true)
	progress.Done(fmt.Sprintf("%d messages", messagesSynced))
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

	progress := output.StartProgress("Finding unread channels", 0)

	// Get channels with unread messages from API
	unreadChannels, err := s.api.GetChannelsWithUnread()
	if err != nil {
		progress.Fail("failed")
		return result, fmt.Errorf("get unread channels: %w", err)
	}
	progress.Done(fmt.Sprintf("%d channels", len(unreadChannels)))

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
	oldest := time.Now().AddDate(0, 0, -opts.Days).Unix()
	oldestTS := fmt.Sprintf("%d.000000", oldest)

	progress = output.StartProgress("Syncing unread histories", len(channelsToSync))
	messagesSynced := s.syncMessagesParallel(store, state, channelsToSync, oldestTS, progress, true)
	progress.Done(fmt.Sprintf("%d messages", messagesSynced))
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

	// Build query to find parent messages with replies. By default we only
	// fetch incomplete local threads; --full forces a true resync of all threads.
	query := `SELECT m.id, m.channel_id, m.reply_count FROM messages m WHERE m.reply_count > 0 AND (m.thread_ts IS NULL OR m.thread_ts = '' OR m.thread_ts = m.id)`
	args := []interface{}{}

	if !opts.Full {
		query += ` AND m.reply_count > (
			SELECT COUNT(*) FROM messages r
			WHERE r.channel_id = m.channel_id
			  AND r.thread_ts = m.id
			  AND r.id != m.id
		)`
	}

	// Filter by channel if specified
	if opts.Channel != "" {
		// Resolve channel name to ID if needed
		channelID := opts.Channel
		if resolved, err := s.api.ResolveChannel(opts.Channel); err == nil {
			channelID = resolved
		}
		query += ` AND m.channel_id = ?`
		args = append(args, channelID)
	}

	if opts.ActiveDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -opts.ActiveDays).Format(time.RFC3339)
		query += ` AND m.timestamp >= ?`
		args = append(args, cutoff)
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
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("read thread rows: %w", err)
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
		sem         = make(chan struct{}, threadWorkerPoolSize)
	)

	progress := output.StartProgress("Syncing thread replies", len(threads))
	for _, ti := range threads {
		wg.Add(1)
		sem <- struct{}{}

		go func(t threadInfo) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				mu.Lock()
				syncedSoFar := totalSynced
				mu.Unlock()
				progress.Add(1, fmt.Sprintf("%d replies", syncedSoFar))
			}()

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
	progress.Done(fmt.Sprintf("%d replies", totalSynced))

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

func (s *Syncer) getCachedActiveChannels(state *db.SyncState, days int) ([]string, bool) {
	if len(state.CachedActiveChannelIDs) == 0 || state.CachedActiveChannelsTime == "" {
		return nil, false
	}
	if state.CachedActiveChannelsDays != days {
		return nil, false
	}

	cachedTime, err := time.Parse(time.RFC3339, state.CachedActiveChannelsTime)
	if err != nil {
		return nil, false
	}
	if time.Since(cachedTime) > activeChannelCacheDuration {
		return nil, false
	}

	return state.CachedActiveChannelIDs, true
}

func recentlySynced(ts string, within time.Duration) bool {
	if ts == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	return time.Since(t) <= within
}

// channelSyncInfo holds info needed to sync a channel
type channelSyncInfo struct {
	ID       string
	LatestTS string
}

// fetchChannelInfoParallel fetches channel info in parallel and returns channels that need syncing
// Sync criteria: latest.ts > our_last_sync (new messages exist) OR never synced
func (s *Syncer) fetchChannelInfoParallel(store *db.Store, state *db.SyncState, channelIDs []string, progress *output.Progress, skipFresh bool) []channelSyncInfo {
	var (
		mu             sync.Mutex
		wg             sync.WaitGroup
		channelsToSync []channelSyncInfo
		skipped        int
		sem            = make(chan struct{}, channelInfoWorkerPoolSize)
	)

	for _, channelID := range channelIDs {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(chID string) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore
			defer progress.Add(1, "")

			mu.Lock()
			lastSynced := state.ChannelLastSynced[chID]
			mu.Unlock()
			if skipFresh && recentlySynced(lastSynced, activeChannelCacheDuration) {
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}

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
			state.ChannelLastSynced[chID] = time.Now().Format(time.RFC3339)
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
func (s *Syncer) syncMessagesParallel(store *db.Store, state *db.SyncState, channels []channelSyncInfo, oldestTS string, progress *output.Progress, includeThreads bool) int {
	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		totalSynced int
		sem         = make(chan struct{}, messageWorkerPoolSize)
		stateMu     sync.Mutex // Separate mutex for state updates
	)

	for _, ch := range channels {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(chInfo channelSyncInfo) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore
			defer func() {
				mu.Lock()
				syncedSoFar := totalSynced
				mu.Unlock()
				progress.Add(1, fmt.Sprintf("%d messages", syncedSoFar))
			}()

			n, err := s.syncChannelMessagesWithLock(store, state, &stateMu, chInfo.ID, oldestTS, includeThreads)
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
func (s *Syncer) syncChannelMessagesWithLock(store *db.Store, state *db.SyncState, stateMu *sync.Mutex, channelID, oldestTS string, includeThreads bool) (int, error) {
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
			if includeThreads && isThreadParent(&msg) && s.threadNeedsReplies(store, channelID, msg.TS, msg.ReplyCount) {
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

func (s *Syncer) syncUsers(store *db.Store, progress *output.Progress) (int, error) {
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
		progress.Add(len(resp.Members), fmt.Sprintf("%d users", count))

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

	s.cacheUser(user.ID, user.Profile.Email, name)

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

func (s *Syncer) syncChannels(store *db.Store, progress *output.Progress) (int, error) {
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
		progress.Add(len(resp.Channels), fmt.Sprintf("%d channels", count))

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
	// Get all channels
	channels, err := store.ListChannels(db.ChannelListOptions{Limit: 100000})
	if err != nil {
		return 0, err
	}

	// Calculate cutoff for active channels filter
	var activeCutoff time.Time
	if activeDays > 0 {
		activeCutoff = time.Now().AddDate(0, 0, -activeDays)
	}

	activeCount := 0
	skippedCount := 0
	channelsToSync := make([]channelSyncInfo, 0, len(channels))

	for _, ch := range channels {
		// Filter by channel filter set (--my-channels + whitelist)
		if channelFilter != nil && !channelFilter[ch.ID] {
			skippedCount++
			continue
		}

		// Filter by active days if specified (and no channel filter)
		if channelFilter == nil && activeDays > 0 {
			lastActivity, ok := channelActivityTime(ch)
			if ok && lastActivity.Before(activeCutoff) {
				skippedCount++
				continue
			}
		}
		activeCount++

		channelsToSync = append(channelsToSync, channelSyncInfo{
			ID: ch.ID,
		})
	}

	if channelFilter != nil || activeDays > 0 {
		output.Info("Syncing %d channels (skipped %d)", activeCount, skippedCount)
	}

	if len(channelsToSync) == 0 {
		return 0, nil
	}

	progress := output.StartProgress("Syncing channel histories", len(channelsToSync))
	count := s.syncMessagesParallel(store, state, channelsToSync, oldestTS, progress, true)
	progress.Done(fmt.Sprintf("%d messages", count))
	return count, nil
}

func channelActivityTime(ch output.Channel) (time.Time, bool) {
	for _, ts := range []string{ch.LastActivity, ch.LastMessageAt} {
		if ts == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, ts)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func isThreadParent(msg *slack.MessageInfo) bool {
	return msg.ReplyCount > 0 && (msg.ThreadTS == "" || msg.ThreadTS == msg.TS)
}

func (s *Syncer) threadNeedsReplies(store *db.Store, channelID, threadTS string, replyCount int) bool {
	if replyCount <= 0 {
		return false
	}

	var localReplies int
	err := store.DB().QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE channel_id = ? AND thread_ts = ? AND id != ?
	`, channelID, threadTS, threadTS).Scan(&localReplies)
	if err != nil {
		output.Debug("Failed to count thread replies for %s: %v", threadTS, err)
		return true
	}
	return localReplies < replyCount
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

	email, name := s.lookupUser(store, msg.User)

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

	_, err := store.DB().Exec(query,
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

func (s *Syncer) lookupUser(store *db.Store, userID string) (string, string) {
	if userID == "" {
		return "", ""
	}

	s.userMu.RLock()
	if user, ok := s.userCache[userID]; ok {
		s.userMu.RUnlock()
		return user.email, user.name
	}
	s.userMu.RUnlock()

	var email, name string
	if err := store.DB().QueryRow("SELECT COALESCE(email, ''), COALESCE(name, '') FROM users WHERE id = ?", userID).Scan(&email, &name); err != nil {
		email = ""
		name = ""
	}
	s.cacheUser(userID, email, name)
	return email, name
}

func (s *Syncer) cacheUser(userID, email, name string) {
	if userID == "" {
		return
	}
	s.userMu.Lock()
	s.userCache[userID] = userIdentity{email: email, name: name}
	s.userMu.Unlock()
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
