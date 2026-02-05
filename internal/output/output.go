package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	enc.Encode(v)
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
		fmt.Printf("\033[32m✓\033[0m %s\n", msg)
	} else {
		fmt.Printf("✓ %s\n", msg)
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
