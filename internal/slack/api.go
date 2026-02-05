package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"slacli/internal/output"
)

const (
	baseURL = "https://slack.com/api"
)

// API is the Slack API client
type API struct {
	client      *http.Client
	rateLimiter *rateLimiter
}

// NewAPI creates a new Slack API client
func NewAPI(client *http.Client) *API {
	return &API{
		client:      client,
		rateLimiter: newRateLimiter(),
	}
}

// Test tests the API connection
func (a *API) Test() error {
	resp, err := a.get("auth.test", nil)
	if err != nil {
		return err
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("API test failed: %s", result.Error)
	}
	return nil
}

// GetAuthInfo returns info about the authenticated user
func (a *API) GetAuthInfo() (*AuthInfo, error) {
	resp, err := a.get("auth.test", nil)
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

// AuthInfo contains authenticated user info
type AuthInfo struct {
	UserID   string
	UserName string
	TeamID   string
	TeamName string
}

// GetChannelInfo fetches info for a single channel by ID
func (a *API) GetChannelInfo(channelID string) (*ChannelInfo, error) {
	params := url.Values{
		"channel": {channelID},
	}

	resp, err := a.get("conversations.info", params)
	if err != nil {
		return nil, err
	}

	var result struct {
		OK      bool        `json:"ok"`
		Error   string      `json:"error,omitempty"`
		Channel ChannelInfo `json:"channel"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("conversations.info: %s", result.Error)
	}
	return &result.Channel, nil
}

// ListChannels returns all channels
func (a *API) ListChannels(cursor string) (*ChannelsResponse, error) {
	params := url.Values{
		"types":           {"public_channel,private_channel,mpim,im"},
		"exclude_archived": {"false"},
		"limit":           {"200"},
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	resp, err := a.get("conversations.list", params)
	if err != nil {
		return nil, err
	}

	var result ChannelsResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("conversations.list: %s", result.Error)
	}
	return &result, nil
}

// ChannelsResponse is the response from conversations.list
type ChannelsResponse struct {
	OK               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	Channels         []ChannelInfo    `json:"channels"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

// ChannelLatest represents the latest message in a channel
type ChannelLatest struct {
	TS string `json:"ts"`
}

// ChannelInfo is a channel from the API
type ChannelInfo struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	IsChannel      bool          `json:"is_channel"`
	IsGroup        bool          `json:"is_group"`
	IsIM           bool          `json:"is_im"`
	IsMPIM         bool          `json:"is_mpim"`
	IsPrivate      bool          `json:"is_private"`
	IsArchived     bool          `json:"is_archived"`
	User           string        `json:"user,omitempty"` // For DMs
	NumMembers     int           `json:"num_members"`
	UnreadCount    int           `json:"unread_count,omitempty"`
	UnreadCountDisplay int       `json:"unread_count_display,omitempty"`
	LastRead       string        `json:"last_read,omitempty"` // Timestamp of last read message
	Updated        float64       `json:"updated,omitempty"`   // Unix timestamp of last message
	Latest         ChannelLatest `json:"latest,omitempty"`    // Latest message (for skip-unchanged)
}

// GetChannelType returns the channel type string
func (c *ChannelInfo) GetChannelType() string {
	if c.IsIM {
		return "dm"
	}
	if c.IsMPIM {
		return "group_dm"
	}
	if c.IsPrivate {
		return "private"
	}
	return "channel"
}

// ListUsers returns all users
func (a *API) ListUsers(cursor string) (*UsersResponse, error) {
	params := url.Values{
		"limit": {"200"},
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	resp, err := a.get("users.list", params)
	if err != nil {
		return nil, err
	}

	var result UsersResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("users.list: %s", result.Error)
	}
	return &result, nil
}

// UsersResponse is the response from users.list
type UsersResponse struct {
	OK               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	Members          []UserInfo       `json:"members"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

// UserInfo is a user from the API
type UserInfo struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	RealName string      `json:"real_name"`
	IsBot    bool        `json:"is_bot"`
	Deleted  bool        `json:"deleted"`
	Profile  UserProfile `json:"profile"`
}

// UserProfile contains user profile info
type UserProfile struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Image48     string `json:"image_48"`
}

// GetHistory returns message history for a channel
func (a *API) GetHistory(channelID, cursor string, limit int, oldest, latest string) (*HistoryResponse, error) {
	params := url.Values{
		"channel": {channelID},
		"limit":   {strconv.Itoa(limit)},
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	if oldest != "" {
		params.Set("oldest", oldest)
	}
	if latest != "" {
		params.Set("latest", latest)
	}

	resp, err := a.get("conversations.history", params)
	if err != nil {
		return nil, err
	}

	var result HistoryResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("conversations.history: %s", result.Error)
	}
	return &result, nil
}

// HistoryResponse is the response from conversations.history
type HistoryResponse struct {
	OK               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	Messages         []MessageInfo    `json:"messages"`
	HasMore          bool             `json:"has_more"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

// GetReplies returns all replies in a thread
func (a *API) GetReplies(channelID, threadTS string, cursor string, limit int) (*RepliesResponse, error) {
	params := url.Values{
		"channel": {channelID},
		"ts":      {threadTS},
		"limit":   {strconv.Itoa(limit)},
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	resp, err := a.get("conversations.replies", params)
	if err != nil {
		return nil, err
	}

	var result RepliesResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("conversations.replies: %s", result.Error)
	}
	return &result, nil
}

// RepliesResponse is the response from conversations.replies
type RepliesResponse struct {
	OK               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	Messages         []MessageInfo    `json:"messages"`
	HasMore          bool             `json:"has_more"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

// MessageInfo is a message from the API
type MessageInfo struct {
	Type       string        `json:"type"`
	Subtype    string        `json:"subtype,omitempty"`
	User       string        `json:"user"`
	Text       string        `json:"text"`
	TS         string        `json:"ts"`
	ThreadTS   string        `json:"thread_ts,omitempty"`
	ReplyCount int           `json:"reply_count,omitempty"`
	Reactions  []ReactionInfo `json:"reactions,omitempty"`
	Edited     *EditedInfo   `json:"edited,omitempty"`
}

// ReactionInfo is a reaction on a message
type ReactionInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// EditedInfo indicates if message was edited
type EditedInfo struct {
	User string `json:"user"`
	TS   string `json:"ts"`
}

// ResponseMetadata contains pagination info
type ResponseMetadata struct {
	NextCursor string `json:"next_cursor"`
}

// ResolveChannel resolves a channel name/email/ID to a channel ID
func (a *API) ResolveChannel(channel string) (string, error) {
	// If already an ID, return it
	if strings.HasPrefix(channel, "C") || strings.HasPrefix(channel, "D") || strings.HasPrefix(channel, "G") {
		return channel, nil
	}

	// Remove # prefix
	channel = strings.TrimPrefix(channel, "#")
	channel = strings.TrimPrefix(channel, "@")

	// Try to find by name in cached channels
	params := url.Values{
		"types": {"public_channel,private_channel,mpim,im"},
		"limit": {"1000"},
	}

	resp, err := a.get("conversations.list", params)
	if err != nil {
		return "", err
	}

	var result ChannelsResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
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

func (a *API) findDMByEmail(email string) (string, error) {
	// First, find the user by email
	resp, err := a.get("users.lookupByEmail", url.Values{"email": {email}})
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
	resp, err = a.post("conversations.open", map[string]interface{}{
		"users": userResult.User.ID,
	})
	if err != nil {
		return "", err
	}

	var dmResult struct {
		OK      bool `json:"ok"`
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

// SendMessage sends a message to a channel
func (a *API) SendMessage(channelID, text, threadTS string) (output.SendResult, error) {
	payload := map[string]interface{}{
		"channel": channelID,
		"text":    text,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	resp, err := a.post("chat.postMessage", payload)
	if err != nil {
		return output.SendResult{}, err
	}

	var result struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return output.SendResult{}, err
	}
	if !result.OK {
		return output.SendResult{}, fmt.Errorf("chat.postMessage: %s", result.Error)
	}

	return output.SendResult{
		Channel:   result.Channel,
		ChannelID: channelID,
		Timestamp: result.TS,
		MessageID: result.TS,
	}, nil
}

// ScheduledMessage represents a scheduled message
type ScheduledMessage struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	Text        string `json:"text"`
	PostAt      int64  `json:"post_at"`
	DateCreated int64  `json:"date_created"`
}

// ScheduleMessage schedules a message for future delivery
func (a *API) ScheduleMessage(channelID, text string, postAt int64, threadTS string) (*ScheduledMessage, error) {
	payload := map[string]interface{}{
		"channel": channelID,
		"text":    text,
		"post_at": postAt,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	resp, err := a.post("chat.scheduleMessage", payload)
	if err != nil {
		return nil, err
	}

	var result struct {
		OK               bool   `json:"ok"`
		Error            string `json:"error,omitempty"`
		ScheduledMsgID   string `json:"scheduled_message_id"`
		Channel          string `json:"channel"`
		PostAt           int64  `json:"post_at"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("chat.scheduleMessage: %s", result.Error)
	}

	return &ScheduledMessage{
		ID:        result.ScheduledMsgID,
		ChannelID: result.Channel,
		Text:      text,
		PostAt:    result.PostAt,
	}, nil
}

// ListScheduledMessages lists scheduled messages for a channel
func (a *API) ListScheduledMessages(channelID string) ([]ScheduledMessage, error) {
	params := url.Values{}
	if channelID != "" {
		params.Set("channel", channelID)
	}

	resp, err := a.get("chat.scheduledMessages.list", params)
	if err != nil {
		return nil, err
	}

	var result struct {
		OK       bool `json:"ok"`
		Error    string `json:"error,omitempty"`
		Messages []struct {
			ID          string `json:"id"`
			ChannelID   string `json:"channel_id"`
			Text        string `json:"text"`
			PostAt      int64  `json:"post_at"`
			DateCreated int64  `json:"date_created"`
		} `json:"scheduled_messages"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("chat.scheduledMessages.list: %s", result.Error)
	}

	messages := make([]ScheduledMessage, len(result.Messages))
	for i, m := range result.Messages {
		messages[i] = ScheduledMessage{
			ID:          m.ID,
			ChannelID:   m.ChannelID,
			Text:        m.Text,
			PostAt:      m.PostAt,
			DateCreated: m.DateCreated,
		}
	}

	return messages, nil
}

// DeleteScheduledMessage deletes a scheduled message
func (a *API) DeleteScheduledMessage(channelID, scheduledMsgID string) error {
	payload := map[string]interface{}{
		"channel":              channelID,
		"scheduled_message_id": scheduledMsgID,
	}

	resp, err := a.post("chat.deleteScheduledMessage", payload)
	if err != nil {
		return err
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("chat.deleteScheduledMessage: %s", result.Error)
	}

	return nil
}

// UploadFile uploads a file to a channel
func (a *API) UploadFile(channelID, filePath, initialComment string) (output.FileUploadResult, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return output.FileUploadResult{}, err
	}
	defer file.Close()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add file
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return output.FileUploadResult{}, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return output.FileUploadResult{}, err
	}

	// Add other fields
	writer.WriteField("channels", channelID)
	if initialComment != "" {
		writer.WriteField("initial_comment", initialComment)
	}

	writer.Close()

	req, err := http.NewRequest("POST", baseURL+"/files.upload", &buf)
	if err != nil {
		return output.FileUploadResult{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	a.rateLimiter.wait()
	resp, err := a.client.Do(req)
	if err != nil {
		return output.FileUploadResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return output.FileUploadResult{}, err
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		File  struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Permalink string `json:"permalink"`
		} `json:"file"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return output.FileUploadResult{}, err
	}
	if !result.OK {
		return output.FileUploadResult{}, fmt.Errorf("files.upload: %s", result.Error)
	}

	return output.FileUploadResult{
		FileID:    result.File.ID,
		FileName:  result.File.Name,
		Permalink: result.File.Permalink,
	}, nil
}

// Draft methods

// ListDrafts returns user's drafts
func (a *API) ListDrafts(limit int) ([]output.Draft, error) {
	// Note: The Slack drafts API is not officially documented
	// This uses an undocumented endpoint that may change
	params := url.Values{
		"limit": {strconv.Itoa(limit)},
	}

	resp, err := a.get("drafts.list", params)
	if err != nil {
		// If endpoint doesn't exist, return empty
		return []output.Draft{}, nil
	}

	var result struct {
		OK     bool `json:"ok"`
		Error  string `json:"error,omitempty"`
		Drafts []struct {
			ID        string `json:"id"`
			ChannelID string `json:"channel_id"`
			Message   struct {
				Text string `json:"text"`
			} `json:"message"`
			ThreadTS  string `json:"thread_ts,omitempty"`
			DateCreate int64 `json:"date_create"`
			DateUpdate int64 `json:"date_update"`
		} `json:"drafts"`
	}

	if err := json.Unmarshal(resp, &result); err != nil {
		return []output.Draft{}, nil
	}

	drafts := make([]output.Draft, 0, len(result.Drafts))
	for _, d := range result.Drafts {
		drafts = append(drafts, output.Draft{
			ID:        d.ID,
			ChannelID: d.ChannelID,
			Text:      d.Message.Text,
			ThreadTS:  d.ThreadTS,
			CreatedAt: time.Unix(d.DateCreate, 0).Format(time.RFC3339),
			UpdatedAt: time.Unix(d.DateUpdate, 0).Format(time.RFC3339),
		})
	}

	return drafts, nil
}

// CreateDraft creates a new draft
func (a *API) CreateDraft(channelID, text, threadTS string) (output.Draft, error) {
	payload := map[string]interface{}{
		"channel_id": channelID,
		"message": map[string]string{
			"text": text,
		},
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	resp, err := a.post("drafts.create", payload)
	if err != nil {
		return output.Draft{}, err
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		Draft struct {
			ID string `json:"id"`
		} `json:"draft"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return output.Draft{}, err
	}
	if !result.OK {
		return output.Draft{}, fmt.Errorf("drafts.create: %s", result.Error)
	}

	return output.Draft{
		ID:        result.Draft.ID,
		ChannelID: channelID,
		Text:      text,
		ThreadTS:  threadTS,
	}, nil
}

// EditDraft edits an existing draft
func (a *API) EditDraft(draftID, text string) (output.Draft, error) {
	payload := map[string]interface{}{
		"draft_id": draftID,
		"message": map[string]string{
			"text": text,
		},
	}

	resp, err := a.post("drafts.edit", payload)
	if err != nil {
		return output.Draft{}, err
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return output.Draft{}, err
	}
	if !result.OK {
		return output.Draft{}, fmt.Errorf("drafts.edit: %s", result.Error)
	}

	return output.Draft{ID: draftID, Text: text}, nil
}

// DeleteDraft deletes a draft
func (a *API) DeleteDraft(draftID string) error {
	payload := map[string]interface{}{
		"draft_id": draftID,
	}

	resp, err := a.post("drafts.delete", payload)
	if err != nil {
		return err
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("drafts.delete: %s", result.Error)
	}

	return nil
}

// SendDraft sends a draft as a message
func (a *API) SendDraft(draftID string) (output.SendResult, error) {
	payload := map[string]interface{}{
		"draft_id": draftID,
	}

	resp, err := a.post("drafts.publish", payload)
	if err != nil {
		return output.SendResult{}, err
	}

	var result struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return output.SendResult{}, err
	}
	if !result.OK {
		return output.SendResult{}, fmt.Errorf("drafts.publish: %s", result.Error)
	}

	return output.SendResult{
		Channel:   result.Channel,
		Timestamp: result.TS,
		MessageID: result.TS,
	}, nil
}

// SearchMessages searches for messages matching a query
func (a *API) SearchMessages(query string, count, page int) (*SearchResponse, error) {
	params := url.Values{
		"query": {query},
		"count": {strconv.Itoa(count)},
		"page":  {strconv.Itoa(page)},
		"sort":  {"timestamp"},
	}

	resp, err := a.get("search.messages", params)
	if err != nil {
		return nil, err
	}

	var result SearchResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("search.messages: %s", result.Error)
	}
	return &result, nil
}

// SearchResponse is the response from search.messages
type SearchResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Messages struct {
		Total      int           `json:"total"`
		Matches    []SearchMatch `json:"matches"`
		Pagination struct {
			TotalCount int `json:"total_count"`
			Page       int `json:"page"`
			PerPage    int `json:"per_page"`
			PageCount  int `json:"page_count"`
		} `json:"pagination"`
	} `json:"messages"`
}

// SearchMatch is a single search result
type SearchMatch struct {
	Channel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"channel"`
	User      string `json:"user"`
	Text      string `json:"text"`
	TS        string `json:"ts"`
	Permalink string `json:"permalink"`
}

// GetChannelsWithUnread returns channels that have unread messages
// Uses conversations.list and filters by unread_count > 0
func (a *API) GetChannelsWithUnread() ([]ChannelInfo, error) {
	var unreadChannels []ChannelInfo
	cursor := ""

	for {
		params := url.Values{
			"types":            {"public_channel,private_channel,mpim,im"},
			"exclude_archived": {"true"},
			"limit":            {"200"},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		resp, err := a.get("conversations.list", params)
		if err != nil {
			return nil, err
		}

		var result ChannelsResponse
		if err := json.Unmarshal(resp, &result); err != nil {
			return nil, err
		}
		if !result.OK {
			return nil, fmt.Errorf("conversations.list: %s", result.Error)
		}

		// conversations.list doesn't return unread_count, need to get via conversations.info
		// Collect channel IDs and fetch info in parallel
		for _, ch := range result.Channels {
			if ch.IsArchived {
				continue
			}
			// Get detailed info including unread count
			info, err := a.GetChannelInfo(ch.ID)
			if err != nil {
				output.Debug("Failed to get info for %s: %v", ch.ID, err)
				continue
			}
			if info.UnreadCountDisplay > 0 || info.UnreadCount > 0 {
				unreadChannels = append(unreadChannels, *info)
			}
		}

		cursor = result.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}

	return unreadChannels, nil
}

// UnreadChannelInfo contains channel info with unread details
type UnreadChannelInfo struct {
	ChannelInfo
	UnreadMessages []MessageInfo
}

// GetMyChannelIDs returns channel IDs where the user has posted messages
// Uses search API with "from:<@userID>" query, paginating through all results
func (a *API) GetMyChannelIDs(days int) ([]string, error) {
	// Get current user ID
	authInfo, err := a.GetAuthInfo()
	if err != nil {
		return nil, fmt.Errorf("get auth info: %w", err)
	}

	// Build search query for messages from current user
	cutoff := time.Now().AddDate(0, 0, -days)
	dateStr := cutoff.Format("2006-01-02")
	query := fmt.Sprintf("from:<@%s> after:%s", authInfo.UserID, dateStr)

	// Extract unique channel IDs across all pages
	channelSet := make(map[string]bool)
	page := 1
	maxPages := 20 // Safety limit

	for page <= maxPages {
		result, err := a.SearchMessages(query, 100, page)
		if err != nil {
			return nil, err
		}

		for _, match := range result.Messages.Matches {
			channelSet[match.Channel.ID] = true
		}

		// Check if more pages
		if page >= result.Messages.Pagination.PageCount || len(result.Messages.Matches) == 0 {
			break
		}
		page++
	}

	channels := make([]string, 0, len(channelSet))
	for id := range channelSet {
		channels = append(channels, id)
	}

	return channels, nil
}

// HTTP helpers

func (a *API) get(method string, params url.Values) ([]byte, error) {
	u := baseURL + "/" + method
	if params != nil {
		u += "?" + params.Encode()
	}

	a.rateLimiter.wait()

	resp, err := a.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle rate limiting
	if resp.StatusCode == 429 {
		retryAfter := resp.Header.Get("Retry-After")
		if secs, err := strconv.Atoi(retryAfter); err == nil {
			time.Sleep(time.Duration(secs) * time.Second)
			return a.get(method, params)
		}
		time.Sleep(time.Second)
		return a.get(method, params)
	}

	return io.ReadAll(resp.Body)
}

func (a *API) post(method string, payload map[string]interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	a.rateLimiter.wait()

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
			return a.post(method, payload)
		}
		time.Sleep(time.Second)
		return a.post(method, payload)
	}

	return io.ReadAll(resp.Body)
}

// Simple rate limiter
type rateLimiter struct {
	mu       sync.Mutex
	lastCall time.Time
	minDelay time.Duration
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		minDelay: 0, // Rely on Slack's 429 response + retry for rate limiting
	}
}

func (r *rateLimiter) wait() {
	r.mu.Lock()
	defer r.mu.Unlock()

	elapsed := time.Since(r.lastCall)
	if elapsed < r.minDelay {
		time.Sleep(r.minDelay - elapsed)
	}
	r.lastCall = time.Now()
}
