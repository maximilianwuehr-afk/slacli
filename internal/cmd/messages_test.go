package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/slack"
)

func TestListMessagesWithThreadFallbackPaginatesAndAppliesLimit(t *testing.T) {
	store := newMessagesTestStore(t)
	const threadTS = "1700000000.000001"
	insertTestMessage(t, store, "C123", threadTS, "", 3, "thread parent")

	pageOne := slack.RepliesResponse{
		OK: true,
		Messages: []slack.MessageInfo{
			{Type: "message", TS: threadTS, Text: "thread parent", ReplyCount: 3},
			{Type: "message", TS: "1700000001.000001", ThreadTS: threadTS, Text: "first reply"},
		},
		HasMore:          false,
		ResponseMetadata: slack.ResponseMetadata{NextCursor: "page-2"},
	}
	pageTwo := slack.RepliesResponse{
		OK: true,
		Messages: []slack.MessageInfo{
			{Type: "message", TS: "1700000002.000001", ThreadTS: threadTS, Text: "second reply"},
			{Type: "message", TS: "1700000003.000001", ThreadTS: threadTS, Text: "third reply"},
		},
	}
	responses := map[string]slack.RepliesResponse{"": pageOne, "page-2": pageTwo}
	var cursors []string
	api := slack.NewAPI(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/conversations.replies" {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		if got := req.URL.Query().Get("channel"); got != "C123" {
			return nil, fmt.Errorf("channel = %q, want C123", got)
		}
		if got := req.URL.Query().Get("ts"); got != threadTS {
			return nil, fmt.Errorf("thread timestamp = %q, want %s", got, threadTS)
		}
		if got := req.URL.Query().Get("limit"); got != "200" {
			return nil, fmt.Errorf("API page limit = %q, want 200", got)
		}
		cursor := req.URL.Query().Get("cursor")
		cursors = append(cursors, cursor)
		response, ok := responses[cursor]
		if !ok {
			return nil, fmt.Errorf("unexpected cursor %q", cursor)
		}
		body, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return jsonHTTPResponse(req, body), nil
	})})

	messages, err := listMessagesWithThreadFallback(store, db.MessageListOptions{
		Channel:  "C123",
		Limit:    2,
		ThreadTS: threadTS,
	}, func() (*slack.API, error) { return api, nil })
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}

	if !reflect.DeepEqual(cursors, []string{"", "page-2"}) {
		t.Fatalf("request cursors = %#v, want first and second pages", cursors)
	}
	if len(messages) != 2 {
		t.Fatalf("returned %d messages, want limit 2", len(messages))
	}
	gotIDs := []string{messages[0].ID, messages[1].ID}
	if want := []string{"1700000003.000001", "1700000002.000001"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("message IDs = %#v, want latest two in descending order %#v", gotIDs, want)
	}
}

func TestListMessagesWithThreadFallbackKeepsCompleteCacheLocal(t *testing.T) {
	store := newMessagesTestStore(t)
	const threadTS = "1700000000.000001"
	insertTestMessage(t, store, "C123", threadTS, "", 1, "thread parent")
	insertTestMessage(t, store, "C123", "1700000001.000001", threadTS, 0, "reply")

	newAPICalled := false
	messages, err := listMessagesWithThreadFallback(store, db.MessageListOptions{
		Channel:  "C123",
		Limit:    10,
		ThreadTS: threadTS,
	}, func() (*slack.API, error) {
		newAPICalled = true
		return nil, fmt.Errorf("unexpected Slack client creation")
	})
	if err != nil {
		t.Fatalf("list cached messages: %v", err)
	}
	if newAPICalled {
		t.Fatal("created a Slack client for a complete cached thread")
	}
	if len(messages) != 2 {
		t.Fatalf("returned %d messages, want parent and reply", len(messages))
	}
}

func TestListMessagesWithThreadFallbackReturnsNonNilEmptyMessages(t *testing.T) {
	store := newMessagesTestStore(t)
	emptyResponse, err := json.Marshal(slack.RepliesResponse{OK: true, Messages: []slack.MessageInfo{}})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	api := slack.NewAPI(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(req, emptyResponse), nil
	})})

	messages, err := listMessagesWithThreadFallback(store, db.MessageListOptions{
		Channel:  "C123",
		Limit:    50,
		ThreadTS: "1700000000.000001",
	}, func() (*slack.API, error) { return api, nil })
	if err != nil {
		t.Fatalf("list empty thread: %v", err)
	}
	if messages == nil {
		t.Fatal("messages is nil; callers must receive an empty slice")
	}
	if len(messages) != 0 {
		t.Fatalf("returned %d messages, want none", len(messages))
	}
}

func TestMessageContextAcceptsMessageIDOrChannelTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		channel   string
		messageID string
		wantErr   bool
	}{
		{name: "message ID", args: []string{"1700000000.000001"}, messageID: "1700000000.000001"},
		{name: "channel and timestamp", args: []string{"#general", "1700000000.000001"}, channel: "#general", messageID: "1700000000.000001"},
		{name: "missing target", args: []string{}, wantErr: true},
		{name: "too many arguments", args: []string{"C123", "1700000000.000001", "extra"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, messageID, err := parseMessageContextArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an argument error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse context args: %v", err)
			}
			if channel != tt.channel || messageID != tt.messageID {
				t.Fatalf("parsed (%q, %q), want (%q, %q)", channel, messageID, tt.channel, tt.messageID)
			}
		})
	}
}

func TestMessageContextChannelMatchesIDOrName(t *testing.T) {
	store := newMessagesTestStore(t)
	const messageID = "1700000000.000001"
	insertTestMessage(t, store, "C123", messageID, "", 0, "target")
	messages, err := store.GetMessageContext(messageID, 0, 0)
	if err != nil {
		t.Fatalf("get message context: %v", err)
	}
	for _, channel := range []string{"C123", "#general"} {
		if !contextContainsChannelMessage(messages, channel, messageID) {
			t.Errorf("message did not match channel %q", channel)
		}
	}
	if contextContainsChannelMessage(messages, "C999", messageID) {
		t.Fatal("message matched the wrong channel ID")
	}
}

func newMessagesTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(&config.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().Exec(
		"INSERT INTO channels (id, name, type) VALUES (?, ?, ?)", "C123", "general", "channel",
	); err != nil {
		t.Fatalf("insert test channel: %v", err)
	}
	return store
}

func insertTestMessage(t *testing.T, store *db.Store, channelID, id, threadTS string, replyCount int, text string) {
	t.Helper()
	if _, err := store.DB().Exec(`
		INSERT INTO messages (id, channel_id, text, timestamp, thread_ts, reply_count, reactions)
		VALUES (?, ?, ?, ?, ?, ?, '[]')`,
		id, channelID, text, slack.TimestampToRFC3339(id), threadTS, replyCount,
	); err != nil {
		t.Fatalf("insert test message %s: %v", id, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonHTTPResponse(req *http.Request, body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    req,
	}
}

func TestThreadFiltersMatchCacheAndLive(t *testing.T) {
	const parent = "1700000000.000001"
	const reply = "1700000002.000001"
	for _, before := range []string{"2023-11-14T22:13:21Z", "2023-11-14T23:13:21+01:00", "1700000001.000001"} {
		t.Run(before, func(t *testing.T) {
			for _, cached := range []bool{false, true} {
				store := newMessagesTestStore(t)
				insertTestMessage(t, store, "C123", parent, "", 1, "parent")
				if cached {
					insertTestMessage(t, store, "C123", reply, parent, 0, "reply")
				}
				// Existing sync stores local-offset RFC3339 values.
				if _, err := store.DB().Exec("UPDATE messages SET timestamp = CASE id WHEN ? THEN ? ELSE ? END", parent, "2023-11-14T23:13:20+01:00", "2023-11-14T23:13:22+01:00"); err != nil {
					t.Fatal(err)
				}
				api := slack.NewAPI(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return jsonHTTPResponse(req, []byte(`{"ok":true,"messages":[{"ts":"1700000000.000001","reply_count":1},{"ts":"1700000002.000001","thread_ts":"1700000000.000001"}]}`)), nil
				})})
				got, err := listMessagesWithThreadFallback(store, db.MessageListOptions{Channel: "C123", ThreadTS: parent, Before: before, Limit: 10}, func() (*slack.API, error) {
					if cached {
						t.Fatal("complete cache required auth")
					}
					return api, nil
				})
				if err != nil || len(got) != 1 || got[0].ID != parent {
					t.Fatalf("cached=%t: got %#v, %v", cached, got, err)
				}
			}
		})
	}
}

func TestCompleteFilteredThreadNeedsNoAuth(t *testing.T) {
	store := newMessagesTestStore(t)
	const parent = "1700000000.000001"
	insertTestMessage(t, store, "C123", parent, "", 1, "parent")
	insertTestMessage(t, store, "C123", "1700000002.000001", parent, 0, "reply")
	got, err := listMessagesWithThreadFallback(store, db.MessageListOptions{Channel: "#general", ThreadTS: parent, After: "2024-01-01T00:00:00Z"}, func() (*slack.API, error) { t.Fatal("empty filtered cache required auth"); return nil, nil })
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v; want []", got, err)
	}
}

func TestThreadFractionalFiltersMatchCacheAndLive(t *testing.T) {
	const parent = "1700000000.000000"
	const early = "1700000000.499999"
	const late = "1700000000.500001"
	for _, cached := range []bool{false, true} {
		for _, boundary := range []string{"1700000000.500000", "2023-11-14T23:13:20.5+01:00"} {
			for _, before := range []bool{false, true} {
				store := newMessagesTestStore(t)
				insertTestMessage(t, store, "C123", parent, "", 2, "parent")
				if cached {
					insertTestMessage(t, store, "C123", early, parent, 0, "early")
					insertTestMessage(t, store, "C123", late, parent, 0, "late")
					// Old cache display timestamps do not retain microseconds.
					if _, err := store.DB().Exec("UPDATE messages SET timestamp = ?", "2023-11-14T23:13:20+01:00"); err != nil {
						t.Fatal(err)
					}
				}
				opts := db.MessageListOptions{Channel: "C123", ThreadTS: parent, Limit: 10}
				if before {
					opts.Before = boundary
				} else {
					opts.After = boundary
				}
				api := slack.NewAPI(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return jsonHTTPResponse(req, []byte(`{"ok":true,"messages":[{"ts":"1700000000.000000","reply_count":2,"user":"U1"},{"ts":"1700000000.499999","thread_ts":"1700000000.000000","user":"U1"},{"ts":"1700000000.500001","thread_ts":"1700000000.000000","user":"U1"}]}`)), nil
				})})
				got, err := listMessagesWithThreadFallback(store, opts, func() (*slack.API, error) {
					if cached {
						t.Fatal("cache required API")
					}
					return api, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				ids := []string{}
				for _, m := range got {
					ids = append(ids, m.ID)
					if !cached && m.AuthorName != "U1" {
						t.Fatalf("live display author missing: %#v", m)
					}
				}
				want := []string{late}
				if before {
					want = []string{early, parent}
				}
				if !reflect.DeepEqual(ids, want) {
					t.Fatalf("cached=%v before=%v boundary=%s: got %v want %v", cached, before, boundary, ids, want)
				}
			}
		}
	}
}
