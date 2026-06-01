package output

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestChannelListResult_JSONSchema(t *testing.T) {
	result := ChannelListResult{
		Channels: []Channel{
			{
				ID:            "C123456",
				Name:          "general",
				Type:          "channel",
				IsPrivate:     false,
				IsArchived:    false,
				LastMessageAt: "2025-01-06T10:30:00Z",
				UnreadCount:   5,
				Members:       []string{"user1@example.com", "user2@example.com"},
			},
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Verify required fields are present
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	channels, ok := parsed["channels"].([]interface{})
	if !ok || len(channels) == 0 {
		t.Fatal("Expected channels array")
	}

	ch := channels[0].(map[string]interface{})
	requiredFields := []string{"id", "name", "type", "is_private", "is_archived", "unread_count"}
	for _, field := range requiredFields {
		if _, ok := ch[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestMessageListResult_JSONSchema(t *testing.T) {
	result := MessageListResult{
		Messages: []Message{
			{
				ID:          "1704540600.123456",
				ChannelID:   "C123456",
				ChannelName: "general",
				AuthorID:    "U123456",
				AuthorEmail: "user@example.com",
				AuthorName:  "Test User",
				Text:        "Hello world",
				Timestamp:   "2025-01-06T10:30:00Z",
				ReplyCount:  3,
				Reactions:   []Reaction{{Name: "thumbsup", Count: 2}},
				Edited:      false,
			},
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Verify required fields
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	messages, ok := parsed["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		t.Fatal("Expected messages array")
	}

	msg := messages[0].(map[string]interface{})
	requiredFields := []string{"id", "channel_id", "author_id", "author_email", "text", "timestamp", "reply_count", "edited"}
	for _, field := range requiredFields {
		if _, ok := msg[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestSearchResult_JSONSchema(t *testing.T) {
	result := SearchResult{
		Messages: []Message{
			{
				ID:          "1704540600.123456",
				ChannelID:   "C123456",
				ChannelName: "general",
				AuthorID:    "U123456",
				AuthorEmail: "user@example.com",
				AuthorName:  "Test User",
				Text:        "Hello world",
				Timestamp:   "2025-01-06T10:30:00Z",
				ReplyCount:  3,
				Edited:      false,
				Source:      "local+live",
			},
		},
		Mode:                "hybrid",
		LocalIndexFreshness: "3d old",
		Timings: SearchTimings{
			Local:    "20ms",
			Live:     "200ms",
			Merge:    "1ms",
			IndexAge: "72h0m0s",
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	for _, field := range []string{"messages", "mode", "local_index_freshness", "timings"} {
		if _, ok := parsed[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}

	messages := parsed["messages"].([]interface{})
	msg := messages[0].(map[string]interface{})
	if msg["source"] != "local+live" {
		t.Errorf("Expected source label, got %v", msg["source"])
	}
}

func TestUserListResult_JSONSchema(t *testing.T) {
	result := UserListResult{
		Users: []User{
			{
				ID:          "U123456",
				Email:       "user@example.com",
				Name:        "Test User",
				DisplayName: "tester",
				IsBot:       false,
			},
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	users, ok := parsed["users"].([]interface{})
	if !ok || len(users) == 0 {
		t.Fatal("Expected users array")
	}

	user := users[0].(map[string]interface{})
	requiredFields := []string{"id", "email", "name", "is_bot"}
	for _, field := range requiredFields {
		if _, ok := user[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestDraftListResult_JSONSchema(t *testing.T) {
	result := DraftListResult{
		Drafts: []Draft{
			{
				ID:        "D123456",
				Channel:   "general",
				ChannelID: "C123456",
				Text:      "Draft message",
				CreatedAt: "2025-01-06T10:00:00Z",
				UpdatedAt: "2025-01-06T10:30:00Z",
			},
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	drafts, ok := parsed["drafts"].([]interface{})
	if !ok || len(drafts) == 0 {
		t.Fatal("Expected drafts array")
	}

	draft := drafts[0].(map[string]interface{})
	requiredFields := []string{"id", "channel", "channel_id", "text", "created_at", "updated_at"}
	for _, field := range requiredFields {
		if _, ok := draft[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestDBStats_JSONSchema(t *testing.T) {
	stats := DBStats{
		SizeBytes:     1024 * 1024 * 10,
		MessageCount:  1000,
		ChannelCount:  50,
		UserCount:     100,
		OldestMessage: "2024-01-01T00:00:00Z",
		NewestMessage: "2025-01-06T10:30:00Z",
	}

	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	requiredFields := []string{"size_bytes", "message_count", "channel_count", "user_count"}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestDoctorResult_JSONSchema(t *testing.T) {
	result := DoctorResult{
		Checks: []DoctorCheck{
			{
				Name:    "Authentication",
				Status:  "ok",
				Message: "Valid token",
			},
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	checks, ok := parsed["checks"].([]interface{})
	if !ok || len(checks) == 0 {
		t.Fatal("Expected checks array")
	}

	check := checks[0].(map[string]interface{})
	requiredFields := []string{"name", "status", "message"}
	for _, field := range requiredFields {
		if _, ok := check[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestSendResult_JSONSchema(t *testing.T) {
	result := SendResult{
		Channel:   "general",
		ChannelID: "C123456",
		Timestamp: "1704540600.123456",
		MessageID: "1704540600.123456",
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	requiredFields := []string{"channel", "channel_id", "timestamp", "message_id"}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestAuthStatus_JSONSchema(t *testing.T) {
	status := AuthStatus{
		TeamID:    "T123456",
		TeamName:  "Test Team",
		UserID:    "U123456",
		UserName:  "testuser",
		ExpiresAt: "2025-02-06T10:30:00Z",
		Status:    "valid",
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	requiredFields := []string{"team_id", "team_name", "user_id", "status"}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

func TestSyncResult_JSONSchema(t *testing.T) {
	result := SyncResult{
		ChannelsSynced: 50,
		MessagesSynced: 1000,
		UsersSynced:    100,
		Duration:       "5m30s",
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	requiredFields := []string{"channels_synced", "messages_synced", "users_synced", "duration"}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("Missing required field: %s", field)
		}
	}
}

// Golden file test helpers
func updateGolden(t *testing.T, name string, data []byte) {
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		goldenPath := "../../testdata/golden/" + name
		if err := os.WriteFile(goldenPath, data, 0644); err != nil {
			t.Fatalf("Failed to write golden file: %v", err)
		}
	}
}

func loadGolden(t *testing.T, name string) []byte {
	goldenPath := "../../testdata/golden/" + name
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("Failed to read golden file: %v", err)
	}
	return data
}

func TestChannelListResult_GoldenJSON(t *testing.T) {
	result := ChannelListResult{
		Channels: []Channel{
			{
				ID:            "C123456",
				Name:          "general",
				Type:          "channel",
				IsPrivate:     false,
				IsArchived:    false,
				LastMessageAt: "2025-01-06T10:30:00Z",
				UnreadCount:   5,
				Members:       []string{"user1@example.com"},
			},
			{
				ID:            "D789",
				Name:          "user@example.com",
				Type:          "dm",
				IsPrivate:     true,
				LastMessageAt: "2025-01-05T14:00:00Z",
				UnreadCount:   0,
			},
		},
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	updateGolden(t, "channels_list.json", data)

	golden := loadGolden(t, "channels_list.json")
	if golden != nil && !bytes.Equal(data, golden) {
		t.Errorf("Output changed. Run with UPDATE_GOLDEN=1 to update.\nGot:\n%s\nWant:\n%s", data, golden)
	}
}

func TestMessageListResult_GoldenJSON(t *testing.T) {
	result := MessageListResult{
		Messages: []Message{
			{
				ID:          "1704540600.123456",
				ChannelID:   "C123456",
				ChannelName: "general",
				AuthorID:    "U123456",
				AuthorEmail: "user@example.com",
				AuthorName:  "Test User",
				Text:        "Hello world",
				Timestamp:   "2025-01-06T10:30:00Z",
				ReplyCount:  3,
				Reactions:   []Reaction{{Name: "thumbsup", Count: 2}},
				Edited:      false,
			},
		},
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	updateGolden(t, "messages_list.json", data)

	golden := loadGolden(t, "messages_list.json")
	if golden != nil && !bytes.Equal(data, golden) {
		t.Errorf("Output changed. Run with UPDATE_GOLDEN=1 to update")
	}
}
