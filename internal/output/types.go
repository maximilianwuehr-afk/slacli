package output

// Channel represents a Slack channel or DM
type Channel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"` // channel, dm, group_dm, private
	IsPrivate     bool     `json:"is_private"`
	IsArchived    bool     `json:"is_archived"`
	LastMessageAt string   `json:"last_message_at,omitempty"`
	LastSentAt    string   `json:"last_sent_at,omitempty"`
	LastMentionAt string   `json:"last_mention_at,omitempty"`
	LastActivity  string   `json:"last_activity,omitempty"` // Slack API's updated timestamp
	UnreadCount   int      `json:"unread_count"`
	Members       []string `json:"members,omitempty"`
}

// ChannelListResult is the output for channel list command
type ChannelListResult struct {
	Channels []Channel `json:"channels"`
}

// Message represents a Slack message
type Message struct {
	ID          string     `json:"id"`
	ChannelID   string     `json:"channel_id"`
	ChannelName string     `json:"channel_name,omitempty"`
	AuthorID    string     `json:"author_id"`
	AuthorEmail string     `json:"author_email"`
	AuthorName  string     `json:"author_name"`
	Text        string     `json:"text"`
	Timestamp   string     `json:"timestamp"`
	ThreadTS    string     `json:"thread_ts,omitempty"`
	ReplyCount  int        `json:"reply_count"`
	Reactions   []Reaction `json:"reactions,omitempty"`
	Edited      bool       `json:"edited"`
}

// Reaction represents a message reaction
type Reaction struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// MessageListResult is the output for message commands
type MessageListResult struct {
	Messages []Message `json:"messages"`
}

// User represents a Slack user
type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	IsBot       bool   `json:"is_bot"`
}

// UserListResult is the output for users list command
type UserListResult struct {
	Users []User `json:"users"`
}

// Draft represents a Slack draft
type Draft struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	Text      string `json:"text"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// DraftListResult is the output for drafts list command
type DraftListResult struct {
	Drafts []Draft `json:"drafts"`
	Source string  `json:"source,omitempty"` // "xoxc" or "scheduled"
}

// DBStats represents database statistics
type DBStats struct {
	SizeBytes     int64  `json:"size_bytes"`
	MessageCount  int    `json:"message_count"`
	ChannelCount  int    `json:"channel_count"`
	UserCount     int    `json:"user_count"`
	OldestMessage string `json:"oldest_message,omitempty"`
	NewestMessage string `json:"newest_message,omitempty"`
}

// PruneResult represents the result of a prune operation
type PruneResult struct {
	DeletedCount int   `json:"deleted_count"`
	BytesFreed   int64 `json:"bytes_freed"`
}

// VacuumResult represents the result of a vacuum operation
type VacuumResult struct {
	SizeBefore int64 `json:"size_before"`
	SizeAfter  int64 `json:"size_after"`
	Reclaimed  int64 `json:"reclaimed"`
}

// DoctorCheck represents a single diagnostic check
type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok, warning, error, skip
	Message string `json:"message"`
}

// DoctorResult represents the result of doctor command
type DoctorResult struct {
	Checks []DoctorCheck `json:"checks"`
}

// AuthStatus represents authentication status
type AuthStatus struct {
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	ExpiresAt string `json:"expires_at"`
	Status    string `json:"status"` // valid, expired, invalid
}

// SyncResult represents the result of a sync operation
type SyncResult struct {
	ChannelsSynced int    `json:"channels_synced"`
	MessagesSynced int    `json:"messages_synced"`
	UsersSynced    int    `json:"users_synced"`
	Duration       string `json:"duration"`
}

// SendResult represents the result of sending a message
type SendResult struct {
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	Timestamp string `json:"timestamp"`
	MessageID string `json:"message_id"`
}

// FileUploadResult represents the result of uploading a file
type FileUploadResult struct {
	FileID    string `json:"file_id"`
	FileName  string `json:"file_name"`
	Permalink string `json:"permalink"`
}
