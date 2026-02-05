package slack

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"slacli/internal/output"
)

// XoxcAPI is the Slack API client using xoxc tokens for internal APIs
type XoxcAPI struct {
	client      *http.Client
	rateLimiter *rateLimiter
	workspace   string // Workspace subdomain for edge API
}

// NewXoxcAPI creates a new xoxc-based API client
func NewXoxcAPI(client *http.Client, workspace string) *XoxcAPI {
	return &XoxcAPI{
		client:      client,
		rateLimiter: newRateLimiter(),
		workspace:   workspace,
	}
}

// Draft represents a Slack draft message
type Draft struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	ConvID      string `json:"conversation_id"` // Alias for channel_id in some responses
	Text        string `json:"text"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	DateCreate  int64  `json:"date_create"`
	DateUpdate  int64  `json:"date_update"`
	DateDelete  int64  `json:"date_delete,omitempty"`
}

// ListDrafts returns all drafts for the authenticated user
func (a *XoxcAPI) ListDrafts() ([]output.Draft, error) {
	// Use the edge API endpoint for drafts
	params := url.Values{
		"include_channel_names": {"true"},
	}

	resp, err := a.post("drafts.list", params)
	if err != nil {
		return nil, fmt.Errorf("drafts.list: %w", err)
	}

	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
		Drafts []struct {
			ID           string `json:"id"`
			DateCreated  int64  `json:"date_created"`
			IsDeleted    bool   `json:"is_deleted"`
			IsSent       bool   `json:"is_sent"`
			Blocks       []struct {
				Type     string `json:"type"`
				Elements []struct {
					Type     string `json:"type"`
					Elements []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"elements"`
				} `json:"elements"`
			} `json:"blocks"`
			Destinations []struct {
				ChannelID string `json:"channel_id"`
				ThreadTS  string `json:"thread_ts,omitempty"`
			} `json:"destinations"`
			LastUpdatedTS string `json:"last_updated_ts"`
		} `json:"drafts"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("drafts.list: %s", result.Error)
	}

	drafts := make([]output.Draft, 0, len(result.Drafts))
	for _, d := range result.Drafts {
		// Skip deleted or sent drafts
		if d.IsDeleted || d.IsSent {
			continue
		}

		// Extract text from blocks
		var text string
		for _, block := range d.Blocks {
			if block.Type == "rich_text" {
				for _, elem := range block.Elements {
					if elem.Type == "rich_text_section" {
						for _, item := range elem.Elements {
							if item.Type == "text" {
								text += item.Text
							}
						}
					}
				}
			}
		}

		// Get channel from destinations
		var channelID, threadTS string
		if len(d.Destinations) > 0 {
			channelID = d.Destinations[0].ChannelID
			threadTS = d.Destinations[0].ThreadTS
		}

		// Parse last_updated_ts (Slack timestamp format: "1234567890.123456")
		var updatedAt time.Time
		if d.LastUpdatedTS != "" {
			parts := strings.Split(d.LastUpdatedTS, ".")
			if len(parts) > 0 {
				if ts, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
					updatedAt = time.Unix(ts, 0)
				}
			}
		}

		drafts = append(drafts, output.Draft{
			ID:        d.ID,
			ChannelID: channelID,
			Text:      text,
			ThreadTS:  threadTS,
			CreatedAt: time.Unix(d.DateCreated, 0).Format(time.RFC3339),
			UpdatedAt: updatedAt.Format(time.RFC3339),
		})
	}

	return drafts, nil
}

// SaveDraft creates or updates a draft
// If draftID is empty, creates a new draft
// Returns the draft ID
func (a *XoxcAPI) SaveDraft(channelID, text, threadTS, draftID string) (string, error) {
	// Generate UUID for client_msg_id
	clientMsgID := generateUUID()

	// Build destinations
	destination := map[string]interface{}{
		"channel_id": channelID,
	}
	if threadTS != "" {
		destination["thread_ts"] = threadTS
	}

	// Build blocks (rich_text format)
	blocks := []map[string]interface{}{
		{
			"type": "rich_text",
			"elements": []map[string]interface{}{
				{
					"type": "rich_text_section",
					"elements": []map[string]interface{}{
						{
							"type": "text",
							"text": text,
						},
					},
				},
			},
		},
	}

	payload := map[string]interface{}{
		"client_msg_id":    clientMsgID,
		"destinations":     []map[string]interface{}{destination},
		"blocks":           blocks,
		"file_ids":         []string{},
		"is_from_composer": false,
	}

	// If updating existing draft, add draft_id
	if draftID != "" {
		payload["draft_id"] = draftID
	}

	resp, err := a.postJSON("drafts.create", payload)
	if err != nil {
		return "", fmt.Errorf("drafts.create: %w", err)
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		Draft struct {
			ID string `json:"id"`
		} `json:"draft"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if !result.OK {
		return "", fmt.Errorf("drafts.create: %s", result.Error)
	}

	return result.Draft.ID, nil
}

// generateUUID generates a random UUID v4
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// DeleteDraft deletes a draft by ID
// Uses a far-future timestamp to force-delete (bypasses conflict checks)
func (a *XoxcAPI) DeleteDraft(channelID, draftID string) error {
	// Use a far-future timestamp to force the delete
	// This bypasses Slack's optimistic concurrency control
	payload := map[string]interface{}{
		"draft_id":               draftID,
		"client_last_updated_ts": "9999999999.999999",
	}

	resp, err := a.postJSON("drafts.delete", payload)
	if err != nil {
		return fmt.Errorf("drafts.delete: %w", err)
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if !result.OK {
		return fmt.Errorf("drafts.delete: %s", result.Error)
	}

	return nil
}

// getDraftLastUpdated fetches a draft's last_updated_ts
func (a *XoxcAPI) getDraftLastUpdated(draftID string) (string, error) {
	params := url.Values{
		"include_channel_names": {"false"},
	}

	resp, err := a.post("drafts.list", params)
	if err != nil {
		return "", err
	}

	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
		Drafts []struct {
			ID            string `json:"id"`
			LastUpdatedTS string `json:"last_updated_ts"`
		} `json:"drafts"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
	}

	if !result.OK {
		return "", fmt.Errorf("drafts.list: %s", result.Error)
	}

	for _, d := range result.Drafts {
		if d.ID == draftID {
			return d.LastUpdatedTS, nil
		}
	}

	return "", fmt.Errorf("draft not found: %s", draftID)
}

// TestAuth verifies the xoxc credentials work
func (a *XoxcAPI) TestAuth() (*AuthInfo, error) {
	resp, err := a.post("auth.test", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
		UserID string `json:"user_id"`
		User   string `json:"user"`
		TeamID string `json:"team_id"`
		Team   string `json:"team"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("auth.test: %s", result.Error)
	}

	return &AuthInfo{
		UserID:   result.UserID,
		UserName: result.User,
		TeamID:   result.TeamID,
		TeamName: result.Team,
	}, nil
}

// ResolveChannel resolves a channel name/email/ID to a channel ID
func (a *XoxcAPI) ResolveChannel(channel string) (string, error) {
	// If already an ID, return it
	if strings.HasPrefix(channel, "C") || strings.HasPrefix(channel, "D") || strings.HasPrefix(channel, "G") {
		return channel, nil
	}

	// Remove # prefix
	channel = strings.TrimPrefix(channel, "#")
	channel = strings.TrimPrefix(channel, "@")

	// Try to find by name
	params := url.Values{
		"types": {"public_channel,private_channel,mpim,im"},
		"limit": {"1000"},
	}

	resp, err := a.post("conversations.list", params)
	if err != nil {
		return "", err
	}

	var result struct {
		OK       bool          `json:"ok"`
		Error    string        `json:"error,omitempty"`
		Channels []ChannelInfo `json:"channels"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
	}

	if !result.OK {
		return "", fmt.Errorf("conversations.list: %s", result.Error)
	}

	for _, ch := range result.Channels {
		if ch.Name == channel {
			return ch.ID, nil
		}
	}

	// If it's an email, try to find the DM
	if strings.Contains(channel, "@") {
		return a.findDMByEmail(channel)
	}

	return "", fmt.Errorf("channel not found: %s", channel)
}

func (a *XoxcAPI) findDMByEmail(email string) (string, error) {
	// First, find the user by email
	params := url.Values{"email": {email}}
	resp, err := a.post("users.lookupByEmail", params)
	if err != nil {
		return "", err
	}

	var userResult struct {
		OK    bool     `json:"ok"`
		Error string   `json:"error,omitempty"`
		User  UserInfo `json:"user"`
	}
	if err := json.Unmarshal(resp, &userResult); err != nil {
		return "", err
	}
	if !userResult.OK {
		return "", fmt.Errorf("user not found: %s", email)
	}

	// Open a DM with the user
	params = url.Values{"users": {userResult.User.ID}}
	resp, err = a.post("conversations.open", params)
	if err != nil {
		return "", err
	}

	var dmResult struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
	}
	if err := json.Unmarshal(resp, &dmResult); err != nil {
		return "", err
	}
	if !dmResult.OK {
		return "", fmt.Errorf("open DM: %s", dmResult.Error)
	}

	return dmResult.Channel.ID, nil
}

// post makes a POST request to the Slack API
func (a *XoxcAPI) post(method string, params url.Values) ([]byte, error) {
	a.rateLimiter.wait()

	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	} else {
		body = strings.NewReader("")
	}

	req, err := http.NewRequest("POST", baseURL+"/"+method, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle rate limiting
	if resp.StatusCode == 429 {
		retryAfter := resp.Header.Get("Retry-After")
		if secs, err := strconv.Atoi(retryAfter); err == nil {
			time.Sleep(time.Duration(secs) * time.Second)
			return a.post(method, params)
		}
		time.Sleep(time.Second)
		return a.post(method, params)
	}

	return io.ReadAll(resp.Body)
}

// postJSON makes a POST request with JSON body
func (a *XoxcAPI) postJSON(method string, payload map[string]interface{}) ([]byte, error) {
	a.rateLimiter.wait()

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle rate limiting
	if resp.StatusCode == 429 {
		retryAfter := resp.Header.Get("Retry-After")
		if secs, err := strconv.Atoi(retryAfter); err == nil {
			time.Sleep(time.Duration(secs) * time.Second)
			return a.postJSON(method, payload)
		}
		time.Sleep(time.Second)
		return a.postJSON(method, payload)
	}

	return io.ReadAll(resp.Body)
}
