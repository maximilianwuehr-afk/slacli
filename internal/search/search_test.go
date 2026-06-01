package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"slacli/internal/output"
)

func TestRunHybridReturnsLocalWhenLiveTimesOut(t *testing.T) {
	localMessage := output.Message{
		ID:        "1000.000001",
		ChannelID: "C1",
		Text:      "local result",
		Timestamp: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}
	local := func(ctx context.Context) ([]output.Message, error) {
		return []output.Message{localMessage}, nil
	}
	live := func(ctx context.Context) ([]output.Message, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	result, err := Run(context.Background(), Options{
		Mode:          ModeHybrid,
		Limit:         10,
		LocalTimeout:  100 * time.Millisecond,
		LiveTimeout:   20 * time.Millisecond,
		HybridTimeout: 100 * time.Millisecond,
	}, local, live)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0].Source != SourceLocal {
		t.Fatalf("expected local source, got %q", result.Messages[0].Source)
	}
	if !containsWarning(result.Warnings, "live search timed out") {
		t.Fatalf("expected live timeout warning, got %#v", result.Warnings)
	}
}

func TestRunHybridMergesDuplicateLocalAndLive(t *testing.T) {
	now := time.Now().UTC()
	ts := "2000.000001"
	local := func(ctx context.Context) ([]output.Message, error) {
		return []output.Message{{
			ID:          ts,
			ChannelID:   "C1",
			ChannelName: "general",
			AuthorEmail: "alice@example.com",
			AuthorName:  "Alice",
			Text:        "local text",
			Timestamp:   now.Format(time.RFC3339),
			ReplyCount:  2,
		}}, nil
	}
	live := func(ctx context.Context) ([]output.Message, error) {
		return []output.Message{{
			ID:        ts,
			ChannelID: "C1",
			AuthorID:  "U1",
			Text:      "live text",
			Timestamp: now.Format(time.RFC3339),
		}}, nil
	}

	result, err := Run(context.Background(), Options{
		Mode:          ModeHybrid,
		Limit:         10,
		LocalTimeout:  time.Second,
		LiveTimeout:   time.Second,
		HybridTimeout: time.Second,
	}, local, live)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]
	if msg.Source != SourceLocalLive {
		t.Fatalf("expected local+live source, got %q", msg.Source)
	}
	if msg.Text != "live text" {
		t.Fatalf("expected recent live text, got %q", msg.Text)
	}
	if msg.AuthorEmail != "alice@example.com" || msg.ReplyCount != 2 {
		t.Fatalf("expected local context to be preserved, got %#v", msg)
	}
}

func TestRunLocalModeDoesNotCallLiveBranch(t *testing.T) {
	liveCalled := false
	local := func(ctx context.Context) ([]output.Message, error) {
		return []output.Message{{ID: "1", ChannelID: "C1", Timestamp: time.Now().UTC().Format(time.RFC3339)}}, nil
	}
	live := func(ctx context.Context) ([]output.Message, error) {
		liveCalled = true
		return nil, errors.New("should not be called")
	}

	result, err := Run(context.Background(), Options{
		Mode:         ModeLocal,
		LocalTimeout: time.Second,
	}, local, live)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if liveCalled {
		t.Fatal("live branch was called in local mode")
	}
	if len(result.Messages) != 1 || result.Messages[0].Source != SourceLocal {
		t.Fatalf("unexpected result: %#v", result.Messages)
	}
}

func TestRunHybridReturnsLiveWhenLocalFails(t *testing.T) {
	local := func(ctx context.Context) ([]output.Message, error) {
		return nil, errors.New("local unavailable")
	}
	live := func(ctx context.Context) ([]output.Message, error) {
		return []output.Message{{ID: "1", ChannelID: "C1", Timestamp: time.Now().UTC().Format(time.RFC3339)}}, nil
	}

	result, err := Run(context.Background(), Options{
		Mode:          ModeHybrid,
		LocalTimeout:  time.Second,
		LiveTimeout:   time.Second,
		HybridTimeout: time.Second,
	}, local, live)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Source != SourceLive {
		t.Fatalf("unexpected result: %#v", result.Messages)
	}
	if !containsWarning(result.Warnings, "local search failed") {
		t.Fatalf("expected local failure warning, got %#v", result.Warnings)
	}
}

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}
