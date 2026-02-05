# slacli Architecture

Technical documentation for developers and contributors.

## System Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                 slacli                                      │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                        CLI Layer (Cobra)                            │   │
│  │                                                                     │   │
│  │  ┌────────┐ ┌────────┐ ┌──────────┐ ┌──────────┐ ┌────────┐        │   │
│  │  │  auth  │ │  sync  │ │ channels │ │ messages │ │  send  │  ...   │   │
│  │  └────┬───┘ └────┬───┘ └────┬─────┘ └────┬─────┘ └────┬───┘        │   │
│  └───────┼──────────┼──────────┼────────────┼────────────┼────────────┘   │
│          │          │          │            │            │                 │
│          ▼          │          ▼            ▼            ▼                 │
│  ┌───────────────┐  │  ┌─────────────────────────────────────────────┐    │
│  │     Auth      │  │  │              Slack API Client               │    │
│  │   ┌───────┐   │  │  │                                             │    │
│  │   │OAuth2 │   │  │  │  conversations.* │ users.* │ chat.* │ files.*│    │
│  │   │ Flow  │   │  │  │                                             │    │
│  │   └───┬───┘   │  │  │  ┌─────────────────────────────────────┐   │    │
│  │       │       │  │  │  │         Rate Limiter                │   │    │
│  │   ┌───▼───┐   │  │  │  └─────────────────────────────────────┘   │    │
│  │   │Tokens │   │  │  └────────────────────┬────────────────────────┘    │
│  │   │Refresh│   │  │                       │                              │
│  │   └───────┘   │  │                       │                              │
│  └───────────────┘  │    ┌──────────────────┴──────────────────┐           │
│                     │    │              Syncer                 │           │
│                     └───►│                                     │           │
│                          │  ┌─────────────┐  ┌─────────────┐   │           │
│                          │  │ Full Sync   │  │ Fast Sync   │   │           │
│                          │  │             │  │(--my-channels)│  │           │
│                          │  └──────┬──────┘  └──────┬──────┘   │           │
│                          │         │                │          │           │
│                          │         └───────┬────────┘          │           │
│                          │                 │                   │           │
│                          │         ┌───────▼───────┐           │           │
│                          │         │ Cursor State  │           │           │
│                          │         └───────────────┘           │           │
│                          └─────────────────┬───────────────────┘           │
│                                            │                               │
│                                            ▼                               │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │                         SQLite Database                              │  │
│  │                                                                      │  │
│  │  ┌────────────────┐  ┌────────────────┐  ┌────────────────────────┐ │  │
│  │  │   channels     │  │    users       │  │      messages          │ │  │
│  │  │                │  │                │  │                        │ │  │
│  │  │ id             │  │ id             │  │ id                     │ │  │
│  │  │ name           │  │ email          │  │ channel_id  ─────────┐ │ │  │
│  │  │ type           │  │ name           │  │ author_id            │ │ │  │
│  │  │ is_private     │  │ display_name   │  │ text                 │ │ │  │
│  │  │ last_message_ts│  │ avatar_url     │  │ timestamp            │ │ │  │
│  │  │ last_sent_ts   │  │ is_bot         │  │ thread_ts            │ │ │  │
│  │  │ last_mention_ts│  │                │  │ reply_count          │ │ │  │
│  │  │ unread_count   │  │                │  │ reactions            │ │ │  │
│  │  └────────────────┘  └────────────────┘  └──────────┬───────────┘ │ │  │
│  │                                                      │             │ │  │
│  │                         ┌────────────────────────────▼───────────┐ │  │
│  │                         │          messages_fts (FTS5)           │ │  │
│  │                         │                                        │ │  │
│  │                         │  Triggers: INSERT / UPDATE / DELETE    │ │  │
│  │                         │  Content: id, channel_id, author_*,    │ │  │
│  │                         │           text, timestamp              │ │  │
│  │                         └────────────────────────────────────────┘ │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                         Output Layer                                │   │
│  │                                                                     │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                 │   │
│  │  │    JSON     │  │    Plain    │  │  Formatted  │                 │   │
│  │  │  (--json)   │  │  (--plain)  │  │   (TTY)     │                 │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘                 │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘

                              File System
┌─────────────────────────────────────────────────────────────────────────────┐
│  ~/.slacli/                                                                 │
│                                                                             │
│  credentials.json     config.json        slacli.db         sync_state.json │
│  (chmod 600)                              (WAL mode)                        │
│                                                                             │
│  ┌─────────────────┐ ┌───────────────┐ ┌─────────────────┐ ┌─────────────┐ │
│  │ access_token    │ │ sync_days: 30 │ │ SQLite + FTS5   │ │ cursors     │ │
│  │ refresh_token   │ │ retention: 90 │ │ ~5MB per 10k    │ │ last_sync   │ │
│  │ expires_at      │ │ whitelist: [] │ │ messages        │ │ user_id     │ │
│  │ token_type      │ │ auto_prune    │ │                 │ │ team_id     │ │
│  └─────────────────┘ └───────────────┘ └─────────────────┘ └─────────────┘ │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Directory Structure

```
slacli/
├── cmd/slack/
│   └── main.go                 # Entry point (binary: slack)
│
├── internal/
│   ├── auth/
│   │   └── auth.go             # OAuth2 flow, token management
│   │
│   ├── cmd/                    # Cobra commands
│   │   ├── root.go             # Root command, global flags
│   │   ├── auth.go             # Login/logout/status
│   │   ├── sync.go             # Sync orchestration
│   │   ├── channels.go         # Channel listing
│   │   ├── messages.go         # List/search/context
│   │   ├── mentions.go         # @mention queries
│   │   ├── send.go             # Send messages
│   │   ├── drafts.go           # Draft CRUD
│   │   ├── users.go            # User listing
│   │   ├── db.go               # DB management
│   │   ├── config.go           # Config commands
│   │   └── doctor.go           # Diagnostics
│   │
│   ├── config/
│   │   └── config.go           # Configuration loading/saving
│   │
│   ├── db/
│   │   ├── db.go               # SQLite layer, FTS5, queries
│   │   └── db_test.go          # Database tests
│   │
│   ├── output/
│   │   ├── output.go           # Formatting logic
│   │   ├── output_test.go      # Formatter tests
│   │   └── types.go            # Data structures
│   │
│   ├── slack/
│   │   └── api.go              # Slack REST client
│   │
│   └── sync/
│       └── sync.go             # Sync orchestration
│
├── go.mod
├── go.sum
├── README.md
└── ARCHITECTURE.md
```

## Component Details

### Auth (`internal/auth/auth.go`)

Handles OAuth2 authentication with Slack.

**Key Functions:**

| Function | Purpose |
|----------|---------|
| `Login()` | Browser-based OAuth flow with local HTTPS callback |
| `GetClient()` | HTTP client with Bearer token injection |
| `Refresh()` | Token refresh before expiry |
| `Status()` | Check authentication state |
| `Logout()` | Clear stored credentials |

**OAuth Flow:**

```
1. Generate state token
2. Start HTTPS server on localhost:49251
3. Open browser to Slack authorize URL
4. User grants permissions
5. Slack redirects to localhost:49251/callback
6. Exchange code for access token
7. Store tokens in credentials.json (chmod 600)
```

**Self-Signed TLS:**

The OAuth callback uses a self-signed certificate for HTTPS. Certificate is generated on-demand, valid for 24 hours, stored in memory only.

### Slack API (`internal/slack/api.go`)

REST client for Slack Web API.

**Endpoints Used:**

| Method | Endpoint | Purpose |
|--------|----------|---------|
| GET | `conversations.list` | List all channels |
| GET | `conversations.history` | Message history |
| GET | `conversations.info` | Channel metadata |
| GET | `users.list` | All workspace users |
| GET | `users.lookupByEmail` | Find user by email |
| POST | `conversations.open` | Open/find DM |
| POST | `chat.postMessage` | Send message |
| POST | `files.upload` | Upload file |
| GET | `search.messages` | Search API (for --my-channels) |
| GET | `auth.test` | Verify token |

**Rate Limiting:**

Simple in-memory rate limiter prevents API throttling. Configurable delay between requests.

**Channel Resolution:**

```go
// "#general" → C01234ABC
func (a *API) ResolveChannel(nameOrID string) (string, error)

// "alice@company.com" → DM channel ID
func (a *API) FindDMByEmail(email string) (string, error)
```

### Sync (`internal/sync/sync.go`)

Orchestrates data synchronization from Slack to local database.

**Two Sync Modes:**

1. **Full Sync** (default)
   - Syncs all users
   - Syncs all channels user has access to
   - Syncs messages for each channel (respects `sync_days`)
   - Uses cursor-based pagination

2. **Fast Sync** (`--my-channels`)
   - Uses Slack search API to find channels user has posted in
   - Adds whitelisted channels
   - Only syncs those channels
   - Much faster for large workspaces

**Sync Options:**

```go
type Options struct {
    Full         bool   // Reset cursors, full re-sync
    ChannelsOnly bool   // Stop after channel metadata
    Days         int    // Message history depth
    ActiveDays   int    // Filter channels by activity
    MyChannels   bool   // Use search API for channel discovery
    Follow       bool   // Continuous sync mode
}
```

**Cursor Management:**

Each channel maintains its own sync cursor (pagination token). Stored in `sync_state.json`:

```json
{
  "last_sync": "2024-01-15T10:30:00Z",
  "user_id": "U123ABC",
  "team_id": "T456DEF",
  "channel_cursors": {
    "C789GHI": "bmV4dF9jdXJzb3I="
  }
}
```

### Database (`internal/db/db.go`)

SQLite database with FTS5 full-text search.

**Schema:**

```sql
-- Channels
CREATE TABLE channels (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,           -- 'channel', 'group', 'im', 'mpim'
    is_private INTEGER DEFAULT 0,
    is_archived INTEGER DEFAULT 0,
    last_message_ts TEXT,
    last_sent_ts TEXT,
    last_mention_ts TEXT,
    last_activity TEXT,
    unread_count INTEGER DEFAULT 0,
    members TEXT,                  -- JSON array
    created_at TEXT,
    updated_at TEXT
);

-- Users
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT,
    name TEXT NOT NULL,
    display_name TEXT,
    avatar_url TEXT,
    is_bot INTEGER DEFAULT 0,
    created_at TEXT,
    updated_at TEXT
);
CREATE INDEX idx_users_email ON users(email);

-- Messages
CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels(id),
    author_id TEXT,
    author_email TEXT,
    author_name TEXT,
    text TEXT,
    timestamp TEXT NOT NULL,
    thread_ts TEXT,
    reply_count INTEGER DEFAULT 0,
    reactions TEXT,                -- JSON
    edited INTEGER DEFAULT 0,
    created_at TEXT
);
CREATE INDEX idx_messages_channel ON messages(channel_id);
CREATE INDEX idx_messages_timestamp ON messages(timestamp);
CREATE INDEX idx_messages_author ON messages(author_email);
CREATE INDEX idx_messages_thread ON messages(thread_ts);

-- FTS5 Virtual Table
CREATE VIRTUAL TABLE messages_fts USING fts5(
    id,
    channel_id,
    author_email,
    author_name,
    text,
    timestamp,
    content='messages',
    content_rowid='rowid'
);

-- Auto-sync triggers
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, id, channel_id, author_email, author_name, text, timestamp)
    VALUES (new.rowid, new.id, new.channel_id, new.author_email, new.author_name, new.text, new.timestamp);
END;

CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, id, channel_id, author_email, author_name, text, timestamp)
    VALUES ('delete', old.rowid, old.id, old.channel_id, old.author_email, old.author_name, old.text, old.timestamp);
END;

CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, id, channel_id, author_email, author_name, text, timestamp)
    VALUES ('delete', old.rowid, old.id, old.channel_id, old.author_email, old.author_name, old.text, old.timestamp);
    INSERT INTO messages_fts(rowid, id, channel_id, author_email, author_name, text, timestamp)
    VALUES (new.rowid, new.id, new.channel_id, new.author_email, new.author_name, new.text, new.timestamp);
END;

-- Drafts
CREATE TABLE drafts (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    text TEXT NOT NULL,
    thread_ts TEXT,
    created_at TEXT,
    updated_at TEXT
);
```

**Key Operations:**

| Function | Purpose |
|----------|---------|
| `ListChannels(opts)` | Query channels with sorting |
| `ListMessages(opts)` | Query messages by channel/time |
| `SearchMessages(opts)` | FTS5 full-text search |
| `GetMentions(opts)` | Find @mentions of current user |
| `GetMessageContext(id, before, after)` | Messages around target |
| `Stats()` | Database statistics |
| `Prune(days)` | Delete old messages |
| `Vacuum()` | Reclaim disk space |

**FTS5 Query Escaping:**

Special characters in search queries are escaped to prevent FTS5 syntax errors:

```go
func escapeFTS(query string) string {
    // Words are quoted to escape special chars
    // "foo bar" → "\"foo\" \"bar\""
}
```

### Config (`internal/config/config.go`)

Configuration management with precedence:

```
CLI flag > Environment variable > Config file > Default
```

**Default Values:**

```go
var defaults = Config{
    SyncDays:      30,
    RetentionDays: 90,
    AutoPrune:     false,
    Whitelist:     []string{},
}
```

**Environment Variables:**

| Variable | Config Key |
|----------|------------|
| `SLACLI_SYNC_DAYS` | sync_days |
| `SLACLI_RETENTION_DAYS` | retention_days |
| `SLACLI_AUTO_PRUNE` | auto_prune |
| `SLACLI_STORE` | data directory |

### Output (`internal/output/`)

Type-safe output formatting.

**Output Modes:**

1. **JSON** (`--json`): Pretty-printed JSON for agents
2. **Plain** (`--plain`): Tab-separated values
3. **Formatted** (default): Terminal-friendly tables

**Dispatch Pattern:**

```go
func Print(result any) error {
    switch v := result.(type) {
    case ChannelListResult:
        return printChannels(v)
    case MessageListResult:
        return printMessages(v)
    case UserListResult:
        return printUsers(v)
    // ...
    }
}
```

**TTY Detection:**

Formatted output auto-detects terminal capability and disables colors when piped.

## Data Flow

### Sync Flow

```
slacli sync --my-channels --days 30
         │
         ▼
    ┌────────────────┐
    │  Load Config   │
    │  Load Creds    │
    │  Open DB       │
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐     GET search.messages
    │ GetMyChannelIDs│────────────────────────►  Slack API
    │ (search API)   │◄────────────────────────
    └───────┬────────┘     Channel IDs where user posted
            │
            ├─── Add whitelisted channels
            │
            ▼
    ┌────────────────┐     GET conversations.info
    │ GetChannelInfo │────────────────────────►  Slack API
    │ (per channel)  │◄────────────────────────
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐
    │ UpsertChannel  │─────────────────────────►  SQLite
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐     GET conversations.history
    │  GetHistory    │────────────────────────►  Slack API
    │ (cursor-based) │◄────────────────────────
    └───────┬────────┘
            │
            ├─── Parse messages, extract users
            │
            ▼
    ┌────────────────┐
    │ UpsertMessage  │─────────────────────────►  SQLite
    │ UpsertUser     │                            (triggers update FTS5)
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐
    │ SaveSyncState  │─────────────────────────►  sync_state.json
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐
    │ Output Result  │─────────────────────────►  stdout
    └────────────────┘
```

### Search Flow

```
slacli messages search "error" --channel "#engineering"
         │
         ▼
    ┌────────────────┐
    │ ResolveChannel │─────────────────────────►  SQLite
    │ "#eng" → ID    │◄─────────────────────────
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐
    │ SearchMessages │
    │                │
    │  SQL:          │
    │  SELECT ...    │
    │  FROM messages │
    │  JOIN messages_fts ON ...
    │  WHERE messages_fts MATCH "error"
    │  AND channel_id = ?
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐
    │ Format Output  │─────────────────────────►  stdout
    │ (JSON/table)   │
    └────────────────┘
```

### Send Flow

```
slacli send "alice@company.com" "Quick question"
         │
         ▼
    ┌────────────────┐
    │  GetClient()   │◄─────────────────────────  credentials.json
    │  (load token)  │
    └───────┬────────┘
            │
            ▼
    ┌────────────────┐     GET users.lookupByEmail
    │ FindDMByEmail  │────────────────────────►  Slack API
    │                │◄────────────────────────
    │                │
    │                │     POST conversations.open
    │                │────────────────────────►  Slack API
    │                │◄────────────────────────
    └───────┬────────┘     DM channel ID
            │
            ▼
    ┌────────────────┐     POST chat.postMessage
    │  SendMessage   │────────────────────────►  Slack API
    │                │◄────────────────────────
    └───────┬────────┘     { ok: true, ts: "..." }
            │
            ▼
    ┌────────────────┐
    │ Output Result  │─────────────────────────►  stdout
    └────────────────┘
```

## Design Decisions

### Pure Go SQLite

Uses `modernc.org/sqlite` instead of CGO-based alternatives:
- No CGO = easier cross-compilation
- Single static binary
- No external dependencies

### WAL Mode

SQLite Write-Ahead Logging enables:
- Concurrent reads during writes
- Better performance for sync operations
- Crash recovery

### FTS5 Triggers

Automatic index maintenance via triggers:
- No manual FTS sync needed
- Consistent search index
- Zero-overhead for queries

### Bearer Token Auth

Simple authentication model:
- Access token stored securely (chmod 600)
- Auto-refresh before expiry
- No session management complexity

### Cursor-Based Pagination

Slack API uses cursor pagination:
- Efficient for large datasets
- Cursors stored per-channel
- Enables incremental sync

### Search API for Fast Sync

`--my-channels` uses Slack's search API:
- Finds channels where user has posted
- Much faster than listing all channels
- Requires `search:read` scope

## Security Considerations

1. **Credentials Storage**: `~/.slacli/credentials.json` with chmod 600
2. **Self-Signed TLS**: Only for localhost OAuth callback
3. **No Hardcoded Secrets**: OAuth credentials from environment
4. **Token Expiry**: Checked before each API call
5. **Local Data**: All data stored locally, no external services

## Performance Characteristics

| Operation | Typical Time | Notes |
|-----------|--------------|-------|
| Full sync (small workspace) | 30-60s | ~100 channels |
| Full sync (large workspace) | 5-10min | ~1000 channels |
| Fast sync (--my-channels) | 10-30s | Depends on activity |
| Local search (FTS5) | <100ms | 100k messages |
| Send message | 200-500ms | API latency |

## Extensibility

### Adding a New Command

1. Create `internal/cmd/<name>.go`
2. Define `&cobra.Command{}`
3. Register in `root.go`: `rootCmd.AddCommand(<name>Cmd)`

### Adding a New API Endpoint

1. Add method to `slack.API` in `internal/slack/api.go`
2. Define request/response types
3. Handle rate limiting if needed

### Adding a New Output Format

1. Add case to `output.Print()` dispatch
2. Implement `print<Type>()` for each output mode

## Testing

```bash
# Unit tests
go test ./...

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Specific package
go test ./internal/db/...

# Verbose
go test -v ./internal/output/...
```

## Build & Release

```bash
# Local build
go build -o slack ./cmd/slack

# With version
go build -ldflags="-X main.version=$(git describe --tags)" -o slack ./cmd/slack

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o slack-linux-amd64 ./cmd/slack
GOOS=darwin GOARCH=arm64 go build -o slack-darwin-arm64 ./cmd/slack
```

CI/CD via GitHub Actions (`.github/workflows/ci.yml`):
- Test on push/PR
- Lint with golangci-lint
- Build multi-platform binaries
- Release on tag push
