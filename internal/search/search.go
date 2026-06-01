package search

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"slacli/internal/output"
)

const (
	DefaultLiveTimeout   = 8 * time.Second
	DefaultLocalTimeout  = 3 * time.Second
	DefaultHybridTimeout = 10 * time.Second

	ModeHybrid Mode = "hybrid"
	ModeLocal  Mode = "local"
	ModeLive   Mode = "live"

	SourceLocal     = "local"
	SourceLive      = "live"
	SourceLocalLive = "local+live"
)

type Mode string

type BranchFunc func(context.Context) ([]output.Message, error)

type LogFunc func(string, ...interface{})

type Options struct {
	Mode                Mode
	Limit               int
	WorkspaceID         string
	LocalIndexFreshness string
	IndexAge            string
	InitialWarnings     []string
	LocalTimeout        time.Duration
	LiveTimeout         time.Duration
	HybridTimeout       time.Duration
	Progress            LogFunc
	Logger              LogFunc
}

type branchSpec struct {
	source  string
	label   string
	timeout time.Duration
	fn      BranchFunc
}

type branchResult struct {
	source   string
	messages []output.Message
	err      error
	duration time.Duration
	timedOut bool
}

// Run executes the selected search branches, applies branch timeouts, then
// merges and dedupes successful results.
func Run(ctx context.Context, opts Options, localFn, liveFn BranchFunc) (output.SearchResult, error) {
	opts = withDefaults(opts)
	result := output.SearchResult{
		Mode:                string(opts.Mode),
		LocalIndexFreshness: opts.LocalIndexFreshness,
		Warnings:            append([]string(nil), opts.InitialWarnings...),
		Timings: output.SearchTimings{
			IndexAge: opts.IndexAge,
		},
	}

	branches, err := selectedBranches(opts, localFn, liveFn)
	if err != nil {
		return result, err
	}

	totalTimeout := opts.HybridTimeout
	if opts.Mode == ModeLocal {
		totalTimeout = opts.LocalTimeout
	}
	if opts.Mode == ModeLive {
		totalTimeout = opts.LiveTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	ch := make(chan branchResult, len(branches))
	pending := make(map[string]time.Time, len(branches))
	for _, branch := range branches {
		if opts.Progress != nil {
			opts.Progress(branch.label)
		}
		pending[branch.source] = time.Now()
		go runBranch(runCtx, branch, ch)
	}

	var branchResults []branchResult
	for len(pending) > 0 {
		select {
		case r := <-ch:
			delete(pending, r.source)
			branchResults = append(branchResults, r)
		case <-runCtx.Done():
			for source, start := range pending {
				branchResults = append(branchResults, branchResult{
					source:   source,
					err:      runCtx.Err(),
					duration: time.Since(start),
					timedOut: true,
				})
			}
			pending = map[string]time.Time{}
		}
	}

	var all []output.Message
	var firstErr error
	successes := 0
	for _, branch := range branchResults {
		setTiming(&result.Timings, branch)
		if branch.err != nil || branch.timedOut {
			if firstErr == nil {
				firstErr = branch.err
				if firstErr == nil {
					firstErr = context.DeadlineExceeded
				}
			}
			result.Warnings = append(result.Warnings, branchWarning(branch))
			continue
		}
		successes++
		all = append(all, branch.messages...)
	}

	if opts.Progress != nil {
		opts.Progress("Merging results...")
	}
	mergeStart := time.Now()
	result.Messages = mergeMessages(all, opts.WorkspaceID, opts.Limit)
	result.Timings.Merge = formatDuration(time.Since(mergeStart))

	logResult(opts.Logger, result)

	if successes == 0 && firstErr != nil {
		return result, firstErr
	}
	return result, nil
}

func withDefaults(opts Options) Options {
	if opts.Mode == "" {
		opts.Mode = ModeHybrid
	}
	if opts.LocalTimeout <= 0 {
		opts.LocalTimeout = DefaultLocalTimeout
	}
	if opts.LiveTimeout <= 0 {
		opts.LiveTimeout = DefaultLiveTimeout
	}
	if opts.HybridTimeout <= 0 {
		opts.HybridTimeout = DefaultHybridTimeout
	}
	return opts
}

func selectedBranches(opts Options, localFn, liveFn BranchFunc) ([]branchSpec, error) {
	local := branchSpec{source: SourceLocal, label: "Searching local index...", timeout: opts.LocalTimeout, fn: localFn}
	live := branchSpec{source: SourceLive, label: "Searching Slack API...", timeout: opts.LiveTimeout, fn: liveFn}

	switch opts.Mode {
	case ModeHybrid:
		if localFn == nil || liveFn == nil {
			return nil, fmt.Errorf("hybrid search requires local and live branches")
		}
		return []branchSpec{local, live}, nil
	case ModeLocal:
		if localFn == nil {
			return nil, fmt.Errorf("local search branch not configured")
		}
		return []branchSpec{local}, nil
	case ModeLive:
		if liveFn == nil {
			return nil, fmt.Errorf("live search branch not configured")
		}
		return []branchSpec{live}, nil
	default:
		return nil, fmt.Errorf("unknown search mode: %s", opts.Mode)
	}
}

func runBranch(parent context.Context, branch branchSpec, ch chan<- branchResult) {
	ctx, cancel := context.WithTimeout(parent, branch.timeout)
	defer cancel()

	start := time.Now()
	messages, err := branch.fn(ctx)
	timedOut := err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded))
	for i := range messages {
		messages[i].Source = branch.source
	}
	ch <- branchResult{
		source:   branch.source,
		messages: messages,
		err:      err,
		duration: time.Since(start),
		timedOut: timedOut,
	}
}

func setTiming(t *output.SearchTimings, branch branchResult) {
	duration := formatDuration(branch.duration)
	switch branch.source {
	case SourceLocal:
		t.Local = duration
	case SourceLive:
		t.Live = duration
	}
}

func branchWarning(branch branchResult) string {
	name := branch.source
	if branch.timedOut || errors.Is(branch.err, context.DeadlineExceeded) {
		return fmt.Sprintf("%s search timed out after %s; returned available results", name, formatDuration(branch.duration))
	}
	if branch.err != nil {
		return fmt.Sprintf("%s search failed: %v; returned available results", name, branch.err)
	}
	return fmt.Sprintf("%s search did not return results; returned available results", name)
}

func mergeMessages(messages []output.Message, workspaceID string, limit int) []output.Message {
	byKey := make(map[string]output.Message, len(messages))
	secondaryToKey := make(map[string]string, len(messages))

	for _, msg := range messages {
		key := dedupeKey(workspaceID, msg)
		secondary := secondaryKey(workspaceID, msg)
		if existingKey, ok := secondaryToKey[secondary]; ok {
			byKey[existingKey] = mergePair(byKey[existingKey], msg)
			continue
		}
		if existing, ok := byKey[key]; ok {
			byKey[key] = mergePair(existing, msg)
			secondaryToKey[secondary] = key
			continue
		}
		byKey[key] = msg
		secondaryToKey[secondary] = key
	}

	merged := make([]output.Message, 0, len(byKey))
	for _, msg := range byKey {
		merged = append(merged, msg)
	}

	sort.SliceStable(merged, func(i, j int) bool {
		left := messageTime(merged[i])
		right := messageTime(merged[j])
		if left.Equal(right) {
			return sourceRank(merged[i].Source) > sourceRank(merged[j].Source)
		}
		return left.After(right)
	})

	if limit > 0 && len(merged) > limit {
		return merged[:limit]
	}
	return merged
}

func mergePair(a, b output.Message) output.Message {
	primary, secondary := choosePrimary(a, b)
	merged := fillMessage(primary, secondary)
	merged.Source = mergeSource(a.Source, b.Source)
	return merged
}

func choosePrimary(a, b output.Message) (output.Message, output.Message) {
	if isLive(b.Source) && isRecent(b) {
		return b, a
	}
	if isLive(a.Source) && isRecent(a) {
		return a, b
	}
	if richness(b) > richness(a) {
		return b, a
	}
	return a, b
}

func fillMessage(primary, secondary output.Message) output.Message {
	if primary.ID == "" {
		primary.ID = secondary.ID
	}
	if primary.ChannelID == "" {
		primary.ChannelID = secondary.ChannelID
	}
	if primary.ChannelName == "" {
		primary.ChannelName = secondary.ChannelName
	}
	if primary.AuthorID == "" {
		primary.AuthorID = secondary.AuthorID
	}
	if primary.AuthorEmail == "" {
		primary.AuthorEmail = secondary.AuthorEmail
	}
	if primary.AuthorName == "" {
		primary.AuthorName = secondary.AuthorName
	}
	if primary.Text == "" {
		primary.Text = secondary.Text
	}
	if primary.Timestamp == "" {
		primary.Timestamp = secondary.Timestamp
	}
	if primary.ThreadTS == "" {
		primary.ThreadTS = secondary.ThreadTS
	}
	if primary.ReplyCount == 0 {
		primary.ReplyCount = secondary.ReplyCount
	}
	if len(primary.Reactions) == 0 {
		primary.Reactions = secondary.Reactions
	}
	primary.Edited = primary.Edited || secondary.Edited
	return primary
}

func mergeSource(a, b string) string {
	hasLocal := strings.Contains(a, SourceLocal) || strings.Contains(b, SourceLocal)
	hasLive := strings.Contains(a, SourceLive) || strings.Contains(b, SourceLive)
	switch {
	case hasLocal && hasLive:
		return SourceLocalLive
	case hasLive:
		return SourceLive
	default:
		return SourceLocal
	}
}

func dedupeKey(workspaceID string, msg output.Message) string {
	return strings.Join([]string{
		workspaceID,
		msg.ChannelID,
		messageTS(msg),
		msg.ThreadTS,
	}, "\x00")
}

func secondaryKey(workspaceID string, msg output.Message) string {
	return strings.Join([]string{
		workspaceID,
		msg.ChannelID,
		messageTS(msg),
	}, "\x00")
}

func messageTS(msg output.Message) string {
	if msg.ID != "" {
		return msg.ID
	}
	return msg.Timestamp
}

func richness(msg output.Message) int {
	score := 0
	for _, value := range []string{msg.ChannelName, msg.AuthorID, msg.AuthorEmail, msg.AuthorName, msg.ThreadTS} {
		if value != "" {
			score++
		}
	}
	if msg.ReplyCount > 0 {
		score++
	}
	if len(msg.Reactions) > 0 {
		score++
	}
	if msg.Edited {
		score++
	}
	return score
}

func isLive(source string) bool {
	return strings.Contains(source, SourceLive)
}

func isRecent(msg output.Message) bool {
	t := messageTime(msg)
	return !t.IsZero() && time.Since(t) <= 7*24*time.Hour
}

func sourceRank(source string) int {
	switch source {
	case SourceLocalLive:
		return 3
	case SourceLive:
		return 2
	case SourceLocal:
		return 1
	default:
		return 0
	}
}

func messageTime(msg output.Message) time.Time {
	if msg.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, msg.Timestamp); err == nil {
			return t
		}
	}
	ts := messageTS(msg)
	if idx := strings.Index(ts, "."); idx > 0 {
		ts = ts[:idx]
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func logResult(logger LogFunc, result output.SearchResult) {
	if logger == nil {
		return
	}
	logger("local search duration: %s", result.Timings.Local)
	logger("live API duration: %s", result.Timings.Live)
	logger("merge duration: %s", result.Timings.Merge)
	logger("index age: %s", result.Timings.IndexAge)
	for _, warning := range result.Warnings {
		logger("search fallback/warning: %s", warning)
	}
}
