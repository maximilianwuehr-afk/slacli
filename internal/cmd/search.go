package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
	searchengine "slacli/internal/search"
	"slacli/internal/slack"
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search messages using local index and Slack API",
	Args:  cobra.ExactArgs(1),
	RunE:  runRootSearch,
}

var (
	searchChannel   string
	searchFrom      string
	searchAfter     string
	searchBefore    string
	searchLimit     int
	searchLocalOnly bool
	searchLiveOnly  bool
)

type searchCLIOptions struct {
	Channel   string
	From      string
	After     string
	Before    string
	Limit     int
	LocalOnly bool
	LiveOnly  bool
}

func init() {
	searchCmd.Flags().StringVar(&searchChannel, "channel", "", "scope to channel/DM")
	searchCmd.Flags().StringVar(&searchFrom, "from", "", "filter by author (email)")
	searchCmd.Flags().StringVar(&searchAfter, "after", "", "messages after date (YYYY-MM-DD)")
	searchCmd.Flags().StringVar(&searchBefore, "before", "", "messages before date")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 50, "max results")
	searchCmd.Flags().BoolVar(&searchLocalOnly, "local", false, "search local index only")
	searchCmd.Flags().BoolVar(&searchLiveOnly, "live", false, "search Slack API only")
}

func runRootSearch(cmd *cobra.Command, args []string) error {
	return runSearch(args[0], searchCLIOptions{
		Channel:   searchChannel,
		From:      searchFrom,
		After:     searchAfter,
		Before:    searchBefore,
		Limit:     searchLimit,
		LocalOnly: searchLocalOnly,
		LiveOnly:  searchLiveOnly,
	})
}

func runSearch(query string, cliOpts searchCLIOptions) error {
	if cliOpts.LocalOnly && cliOpts.LiveOnly {
		return fmt.Errorf("--local and --live are mutually exclusive")
	}

	mode := searchengine.ModeHybrid
	if cliOpts.LocalOnly {
		mode = searchengine.ModeLocal
	}
	if cliOpts.LiveOnly {
		mode = searchengine.ModeLive
	}

	cfg := config.Get()
	freshness, indexAge, warnings := localIndexStatus(cfg)
	if mode == searchengine.ModeLive {
		freshness = ""
		indexAge = ""
		warnings = nil
	}

	result, err := searchengine.Run(context.Background(), searchengine.Options{
		Mode:                mode,
		Limit:               cliOpts.Limit,
		WorkspaceID:         workspaceID(cfg),
		LocalIndexFreshness: freshness,
		IndexAge:            indexAge,
		InitialWarnings:     warnings,
		Progress:            output.Info,
		Logger:              output.Debug,
	}, localSearchFunc(cfg, query, cliOpts), liveSearchFunc(cfg, query, cliOpts))

	output.Print(result)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	return nil
}

func localSearchFunc(cfg *config.Config, query string, cliOpts searchCLIOptions) searchengine.BranchFunc {
	return func(ctx context.Context) ([]output.Message, error) {
		store, err := db.Open(cfg)
		if err != nil {
			return nil, fmt.Errorf("database error: %w", err)
		}
		defer func() { _ = store.Close() }()

		return store.SearchMessagesContext(ctx, db.SearchOptions{
			Query:   query,
			Channel: cliOpts.Channel,
			From:    cliOpts.From,
			After:   cliOpts.After,
			Before:  cliOpts.Before,
			Limit:   cliOpts.Limit,
		})
	}
}

func liveSearchFunc(cfg *config.Config, query string, cliOpts searchCLIOptions) searchengine.BranchFunc {
	return func(ctx context.Context) ([]output.Message, error) {
		client, err := auth.GetClientContext(ctx, cfg, searchengine.DefaultLiveTimeout)
		if err != nil {
			return nil, err
		}
		api := slack.NewAPI(client)

		liveQuery, channelIDFilter, channelNameFilter, err := buildLiveQuery(ctx, api, query, cliOpts)
		if err != nil {
			return nil, err
		}

		count := cliOpts.Limit
		if count <= 0 {
			count = 50
		}
		if channelIDFilter != "" && count < 100 {
			count = min(count*3, 100)
		}

		resp, err := api.SearchMessagesContext(ctx, liveQuery, count, 1)
		if err != nil {
			return nil, err
		}

		messages := make([]output.Message, 0, len(resp.Messages.Matches))
		for _, match := range resp.Messages.Matches {
			if channelIDFilter != "" && match.Channel.ID != channelIDFilter {
				continue
			}
			if channelNameFilter != "" && !strings.EqualFold(match.Channel.Name, channelNameFilter) {
				continue
			}

			messages = append(messages, output.Message{
				ID:          match.TS,
				ChannelID:   match.Channel.ID,
				ChannelName: match.Channel.Name,
				AuthorID:    match.User,
				AuthorName:  match.Username,
				Text:        match.Text,
				Timestamp:   slack.TimestampToRFC3339(match.TS),
				ThreadTS:    match.ThreadTS,
			})
			if cliOpts.Limit > 0 && len(messages) >= cliOpts.Limit {
				break
			}
		}

		return messages, nil
	}
}

func buildLiveQuery(ctx context.Context, api *slack.API, query string, cliOpts searchCLIOptions) (string, string, string, error) {
	parts := []string{query}
	var channelIDFilter string
	var channelNameFilter string

	if cliOpts.Channel != "" {
		channel := strings.TrimPrefix(strings.TrimPrefix(cliOpts.Channel, "#"), "@")
		switch {
		case looksLikeChannelID(channel):
			channelIDFilter = channel
		case strings.Contains(channel, "@"):
			// Slack search does not expose a stable DM email operator. Keep this
			// filter on the local branch and let live search use the text query.
		default:
			channelNameFilter = channel
			parts = append(parts, "in:"+channel)
		}
	}

	if cliOpts.From != "" {
		user, err := api.GetUserByEmailContext(ctx, cliOpts.From)
		if err != nil {
			return "", "", "", fmt.Errorf("resolve author: %w", err)
		}
		parts = append(parts, fmt.Sprintf("from:<@%s>", user.ID))
	}

	if cliOpts.After != "" {
		parts = append(parts, "after:"+cliOpts.After)
	}
	if cliOpts.Before != "" {
		parts = append(parts, "before:"+cliOpts.Before)
	}

	return strings.Join(parts, " "), channelIDFilter, channelNameFilter, nil
}

func localIndexStatus(cfg *config.Config) (string, string, []string) {
	state, err := db.LoadSyncState(cfg)
	if err != nil || state.LastSync == "" {
		return "never synced", "", []string{"Local index has never been synced. Run slack sync --recent --days 7"}
	}

	lastSync, err := time.Parse(time.RFC3339, state.LastSync)
	if err != nil {
		return "unknown", "", []string{"Local index freshness is unknown. Run slack sync --recent --days 7"}
	}

	age := time.Since(lastSync)
	if age < 0 {
		age = 0
	}
	ageLabel := formatIndexAge(age)
	freshness := ageLabel + " old"
	var warnings []string
	if age >= 24*time.Hour {
		warnings = append(warnings, fmt.Sprintf("Local index is %s old. Run slack sync --recent --days 7", ageLabel))
	}
	return freshness, age.Round(time.Second).String(), warnings
}

func workspaceID(cfg *config.Config) string {
	if state, err := db.LoadSyncState(cfg); err == nil && state.TeamID != "" {
		return state.TeamID
	}
	if creds, err := auth.LoadCredentials(cfg); err == nil {
		return creds.TeamID
	}
	return ""
}

func looksLikeChannelID(channel string) bool {
	return strings.HasPrefix(channel, "C") || strings.HasPrefix(channel, "D") || strings.HasPrefix(channel, "G")
}

func formatIndexAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
