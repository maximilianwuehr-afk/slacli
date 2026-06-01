package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// Options configures output formatting
type Options struct {
	JSON    bool
	Plain   bool
	Quiet   bool
	Verbose bool
	NoColor bool
}

var opts Options

// Setup configures the output formatter
func Setup(o Options) {
	opts = o
}

// IsTTY returns true if stdout is a terminal
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// Print outputs data in the configured format
func Print(v interface{}) {
	if opts.Quiet {
		return
	}

	if opts.JSON || !IsTTY() {
		printJSON(v)
		return
	}

	if opts.Plain {
		printPlain(v)
		return
	}

	printFormatted(v)
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "output error: %v\n", err)
	}
}

func printPlain(v interface{}) {
	switch val := v.(type) {
	case ChannelListResult:
		for _, ch := range val.Channels {
			fmt.Printf("%s\t%s\t%s\n", ch.ID, ch.Name, ch.Type)
		}
	case MessageListResult:
		for _, msg := range val.Messages {
			fmt.Printf("%s\t%s\t%s\t%s\n", msg.ID, msg.Timestamp, msg.AuthorEmail, msg.Text)
		}
	case SearchResult:
		if val.LocalIndexFreshness != "" {
			fmt.Printf("# local_index_freshness\t%s\n", val.LocalIndexFreshness)
		}
		for _, msg := range val.Messages {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", msg.ID, msg.Timestamp, msg.Source, msg.AuthorEmail, msg.Text)
		}
	case UserListResult:
		for _, u := range val.Users {
			fmt.Printf("%s\t%s\t%s\n", u.ID, u.Email, u.Name)
		}
	case DraftListResult:
		for _, d := range val.Drafts {
			fmt.Printf("%s\t%s\t%s\n", d.ID, d.Channel, truncate(d.Text, 50))
		}
	case DBStats:
		fmt.Printf("size_bytes\t%d\n", val.SizeBytes)
		fmt.Printf("message_count\t%d\n", val.MessageCount)
		fmt.Printf("channel_count\t%d\n", val.ChannelCount)
		fmt.Printf("user_count\t%d\n", val.UserCount)
	default:
		// Fallback to JSON for unknown types
		printJSON(v)
	}
}

func printFormatted(v interface{}) {
	switch val := v.(type) {
	case ChannelListResult:
		printChannelList(val)
	case MessageListResult:
		printMessageList(val)
	case SearchResult:
		printSearchResult(val)
	case UserListResult:
		printUserList(val)
	case DraftListResult:
		printDraftList(val)
	case DBStats:
		printDBStats(val)
	case VacuumResult:
		printVacuumResult(val)
	case DoctorResult:
		printDoctorResult(val)
	case AuthStatus:
		printAuthStatus(val)
	case SyncResult:
		printSyncResult(val)
	case SendResult:
		printSendResult(val)
	case Draft:
		printDraft(val)
	default:
		printJSON(v)
	}
}

func printChannelList(r ChannelListResult) {
	if len(r.Channels) == 0 {
		fmt.Println("No channels found")
		return
	}

	fmt.Printf("%-12s %-30s %-10s %s\n", "ID", "NAME", "TYPE", "LAST MESSAGE")
	fmt.Println(strings.Repeat("-", 80))
	for _, ch := range r.Channels {
		lastMsg := "-"
		if ch.LastMessageAt != "" {
			lastMsg = formatTime(ch.LastMessageAt)
		}
		name := ch.Name
		if ch.IsPrivate {
			name = "🔒 " + name
		}
		fmt.Printf("%-12s %-30s %-10s %s\n", ch.ID, truncate(name, 28), ch.Type, lastMsg)
	}
}

func printMessageList(r MessageListResult) {
	if len(r.Messages) == 0 {
		fmt.Println("No messages found")
		return
	}

	for _, msg := range r.Messages {
		timestamp := formatTime(msg.Timestamp)
		author := msg.AuthorName
		if author == "" {
			author = msg.AuthorEmail
		}

		if !opts.NoColor {
			fmt.Printf("\033[90m%s\033[0m \033[1m%s\033[0m\n", timestamp, author)
		} else {
			fmt.Printf("%s %s\n", timestamp, author)
		}
		fmt.Println(msg.Text)
		if msg.ReplyCount > 0 {
			fmt.Printf("  └─ %d replies\n", msg.ReplyCount)
		}
		fmt.Println()
	}
}

func printSearchResult(r SearchResult) {
	if r.LocalIndexFreshness != "" {
		fmt.Printf("Local index: %s\n", r.LocalIndexFreshness)
	}
	for _, warning := range r.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}
	if r.LocalIndexFreshness != "" || len(r.Warnings) > 0 {
		fmt.Println()
	}

	if len(r.Messages) == 0 {
		fmt.Println("No messages found")
		return
	}

	for _, msg := range r.Messages {
		timestamp := formatTime(msg.Timestamp)
		author := msg.AuthorName
		if author == "" {
			author = msg.AuthorEmail
		}
		if author == "" {
			author = msg.AuthorID
		}
		channel := msg.ChannelName
		if channel != "" {
			channel = "#" + channel
		} else {
			channel = msg.ChannelID
		}
		source := msg.Source
		if source == "" {
			source = "unknown"
		}

		if !opts.NoColor {
			fmt.Printf("\033[90m%s\033[0m \033[1m%s\033[0m %s [%s]\n", timestamp, author, channel, source)
		} else {
			fmt.Printf("%s %s %s [%s]\n", timestamp, author, channel, source)
		}
		fmt.Println(msg.Text)
		if msg.ReplyCount > 0 {
			fmt.Printf("  └─ %d replies\n", msg.ReplyCount)
		}
		fmt.Println()
	}
}

func printUserList(r UserListResult) {
	if len(r.Users) == 0 {
		fmt.Println("No users found")
		return
	}

	fmt.Printf("%-12s %-30s %s\n", "ID", "EMAIL", "NAME")
	fmt.Println(strings.Repeat("-", 70))
	for _, u := range r.Users {
		fmt.Printf("%-12s %-30s %s\n", u.ID, u.Email, u.Name)
	}
}

func printDraftList(r DraftListResult) {
	if len(r.Drafts) == 0 {
		fmt.Println("No drafts found")
		return
	}

	for _, d := range r.Drafts {
		fmt.Printf("ID: %s\n", d.ID)
		fmt.Printf("Channel: %s\n", d.Channel)
		fmt.Printf("Text: %s\n", truncate(d.Text, 100))
		fmt.Println()
	}
}

func printDBStats(s DBStats) {
	fmt.Printf("Database: %.1f MB\n", float64(s.SizeBytes)/(1024*1024))
	fmt.Printf("Messages: %d\n", s.MessageCount)
	fmt.Printf("Channels: %d\n", s.ChannelCount)
	fmt.Printf("Users: %d\n", s.UserCount)
	if s.OldestMessage != "" {
		fmt.Printf("Oldest: %s\n", s.OldestMessage)
	}
	if s.NewestMessage != "" {
		fmt.Printf("Newest: %s\n", s.NewestMessage)
	}
}

func printVacuumResult(r VacuumResult) {
	fmt.Printf("Size before: %.1f MB\n", float64(r.SizeBefore)/(1024*1024))
	fmt.Printf("Size after: %.1f MB\n", float64(r.SizeAfter)/(1024*1024))
	fmt.Printf("Reclaimed: %.1f MB\n", float64(r.Reclaimed)/(1024*1024))
}

func printDoctorResult(r DoctorResult) {
	for _, check := range r.Checks {
		var status string
		switch check.Status {
		case "ok":
			if !opts.NoColor {
				status = "\033[32m✓\033[0m"
			} else {
				status = "✓"
			}
		case "warning":
			if !opts.NoColor {
				status = "\033[33m⚠\033[0m"
			} else {
				status = "⚠"
			}
		case "error":
			if !opts.NoColor {
				status = "\033[31m✗\033[0m"
			} else {
				status = "✗"
			}
		case "skip":
			status = "-"
		}
		fmt.Printf("%s %s: %s\n", status, check.Name, check.Message)
	}
}

func printAuthStatus(s AuthStatus) {
	fmt.Printf("Team: %s (%s)\n", s.TeamName, s.TeamID)
	fmt.Printf("User: %s (%s)\n", s.UserName, s.UserID)
	fmt.Printf("Expires: %s\n", s.ExpiresAt)
	fmt.Printf("Status: %s\n", s.Status)
}

func printSyncResult(r SyncResult) {
	fmt.Printf("Channels synced: %d\n", r.ChannelsSynced)
	fmt.Printf("Messages synced: %d\n", r.MessagesSynced)
	fmt.Printf("Users synced: %d\n", r.UsersSynced)
	fmt.Printf("Duration: %s\n", r.Duration)
}

func printSendResult(r SendResult) {
	fmt.Printf("Message sent to %s\n", r.Channel)
	fmt.Printf("Timestamp: %s\n", r.Timestamp)
}

func printDraft(d Draft) {
	fmt.Printf("Draft created: %s\n", d.ID)
	fmt.Printf("Channel: %s\n", d.Channel)
}

// Info prints an informational message to stderr
func Info(format string, args ...interface{}) {
	if opts.Quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Progress renders a lightweight stderr spinner for long-running operations.
type Progress struct {
	label   string
	total   int
	done    int
	detail  string
	start   time.Time
	enabled bool

	mu     sync.Mutex
	stop   chan struct{}
	closed bool
	frame  int
}

// StartProgress starts a progress indicator. It is disabled for quiet/json
// output and for non-interactive terminals, keeping stdout machine-readable.
func StartProgress(label string, total int) *Progress {
	p := &Progress{
		label:   label,
		total:   total,
		start:   time.Now(),
		enabled: !opts.Quiet && !opts.JSON && term.IsTerminal(int(os.Stderr.Fd())),
		stop:    make(chan struct{}),
	}
	if opts.Quiet || opts.JSON {
		return p
	}
	if !p.enabled {
		if total > 0 {
			Info("%s (%d)", label, total)
		} else {
			Info("%s...", label)
		}
		return p
	}

	p.render()
	go func() {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.mu.Lock()
				if p.closed {
					p.mu.Unlock()
					return
				}
				p.frame++
				p.renderLocked()
				p.mu.Unlock()
			case <-p.stop:
				return
			}
		}
	}()

	return p
}

// Add advances progress by delta.
func (p *Progress) Add(delta int, detail string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.done += delta
	if detail != "" {
		p.detail = detail
	}
	if p.enabled {
		p.renderLocked()
	}
}

// Done stops the progress indicator and prints a final line.
func (p *Progress) Done(detail string) {
	p.finish("✓", detail)
}

// Fail stops the progress indicator and prints a failure line.
func (p *Progress) Fail(detail string) {
	p.finish("✗", detail)
}

func (p *Progress) finish(mark, detail string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	close(p.stop)
	if detail != "" {
		p.detail = detail
	}
	if !p.enabled {
		return
	}
	p.clearLine()
	status := p.statusLocked()
	if status != "" {
		status = " " + status
	}
	fmt.Fprintf(os.Stderr, "%s %s%s in %s\n", mark, p.label, status, time.Since(p.start).Round(time.Second))
}

func (p *Progress) render() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renderLocked()
}

func (p *Progress) renderLocked() {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	p.clearLine()
	status := p.statusLocked()
	if status != "" {
		status = " " + status
	}
	fmt.Fprintf(os.Stderr, "%s %s%s", frames[p.frame%len(frames)], p.label, status)
}

func (p *Progress) statusLocked() string {
	parts := []string{}
	if p.total > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", p.done, p.total))
	}
	if p.detail != "" {
		parts = append(parts, p.detail)
	}
	return strings.Join(parts, " · ")
}

func (p *Progress) clearLine() {
	fmt.Fprint(os.Stderr, "\r\033[2K")
}

// Debug prints a debug message to stderr (only if verbose)
func Debug(format string, args ...interface{}) {
	if !opts.Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
}

// Error prints an error message to stderr
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}

// Success prints a success message
func Success(msg string) {
	if opts.Quiet {
		return
	}
	if !opts.NoColor && IsTTY() {
		fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", msg)
	} else {
		fmt.Fprintf(os.Stderr, "✓ %s\n", msg)
	}
}

// Warn prints a warning message
func Warn(msg string) {
	if opts.Quiet {
		return
	}
	if !opts.NoColor && IsTTY() {
		fmt.Fprintf(os.Stderr, "\033[33m⚠\033[0m %s\n", msg)
	} else {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", msg)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func formatTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("2006-01-02 15:04")
}
