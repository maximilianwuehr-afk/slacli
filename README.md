# slacli

Agent-native Slack CLI with local full-text search, sync, and messaging.

```
┌──────────────────────────────────────────────────────────────────────┐
│  slacli                                                              │
│  ├── sync ──────► Slack API ──────► SQLite + FTS5                   │
│  ├── search ────► Local FTS5 + Slack API ─► Merged results          │
│  └── send ──────► Slack API ──────► Message posted                  │
└──────────────────────────────────────────────────────────────────────┘
```

## Features

- **Hybrid Search** — Local SQLite FTS5 and Slack API search run in parallel, then merge and dedupe
- **Local Full-Text Search** — SQLite FTS5 indexes synced messages for instant offline search
- **Cursor-Based Sync** — Efficient incremental sync; only fetches new messages
- **Fast Sync Mode** — `--my-channels` uses Slack search API to sync only channels you've posted in
- **Agent-Friendly** — `--json` output for LLM/agent consumption
- **OAuth2 Auth** — Browser-based authentication with automatic token refresh
- **Draft Support** — Create, edit, and send drafts from CLI
- **All Channel Types** — Public, private, DMs, group DMs, and threads

## Quick Start

```bash
# Keep the installed binary current
slacli upgrade

# Set credentials (see Setup section)
export SLACLI_CLIENT_ID="your-client-id"
export SLACLI_CLIENT_SECRET="your-client-secret"

# Authenticate
slacli auth

# Sync your channels
slacli sync --my-channels

# Search
slacli search "deployment"
```

## Architecture

```
┌────────────────────────────────────────────────────────────────────────────┐
│                              CLI Layer (Cobra)                             │
│   auth  │  sync  │  channels  │  messages  │  send  │  drafts  │  db      │
└────┬────┴────┬───┴─────┬──────┴──────┬─────┴────┬───┴────┬─────┴────┬─────┘
     │         │         │             │          │        │          │
     ▼         ▼         ▼             ▼          ▼        ▼          ▼
┌─────────┐ ┌─────────────────────────────────────────────────────┐ ┌───────┐
│  Auth   │ │                    Slack API Client                 │ │Config │
│ OAuth2  │ │  conversations.*  │  users.*  │  chat.*  │  files.* │ │ Mgmt  │
│ Tokens  │ │  + Rate Limiter   │           │          │          │ │       │
└────┬────┘ └─────────────────────────┬───────────────────────────┘ └───┬───┘
     │                                │                                 │
     │                    ┌───────────┴───────────┐                     │
     │                    ▼                       ▼                     │
     │             ┌────────────┐          ┌────────────┐               │
     │             │   Syncer   │          │   Syncer   │               │
     │             │  (Full)    │          │  (--my-ch) │               │
     │             └─────┬──────┘          └──────┬─────┘               │
     │                   │                        │                     │
     │                   └───────────┬────────────┘                     │
     │                               ▼                                  │
     │    ┌──────────────────────────────────────────────────────────┐  │
     │    │                    SQLite Database                       │  │
     │    │  ┌──────────┐  ┌──────────┐  ┌─────────────────────────┐ │  │
     │    │  │ channels │  │  users   │  │       messages          │ │  │
     │    │  └──────────┘  └──────────┘  └────────────┬────────────┘ │  │
     │    │                                           │              │  │
     │    │                              ┌────────────▼────────────┐ │  │
     │    │                              │  messages_fts (FTS5)    │ │  │
     │    │                              │  (auto-synced triggers) │ │  │
     │    │                              └─────────────────────────┘ │  │
     │    └──────────────────────────────────────────────────────────┘  │
     │                                                                  │
     ▼                                                                  ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                          ~/.slacli/                                      │
│  credentials.json (600)  │  slacli.db  │  config.json  │  sync_state.json│
└──────────────────────────────────────────────────────────────────────────┘
```

### Component Overview

| Component | Location | Responsibility |
|-----------|----------|----------------|
| **CLI** | `internal/cmd/` | Cobra commands, flags, user I/O |
| **Auth** | `internal/auth/` | OAuth2 flow, token storage/refresh |
| **Slack API** | `internal/slack/` | REST client, rate limiting |
| **Syncer** | `internal/sync/` | Sync orchestration, cursor management |
| **Database** | `internal/db/` | SQLite + FTS5, queries, migrations |
| **Output** | `internal/output/` | JSON/plain/formatted rendering |
| **Config** | `internal/config/` | Settings, paths, env var handling |

## Installation

### From Binary

Download from [Releases](https://github.com/maximilianwuehr-afk/slacli/releases).

**Platforms:** Linux (amd64/arm64), macOS (amd64/arm64), Windows (amd64)

### From Source

```bash
git clone https://github.com/maximilianwuehr-afk/slacli.git
cd slacli
go build -o slacli ./cmd/slack
```

## Setup

### 1. Create Slack App

1. Go to [api.slack.com/apps](https://api.slack.com/apps)
2. Click **Create New App** → **From scratch**
3. Name it (e.g., `slacli`) and select your workspace
4. Go to **OAuth & Permissions** → **Redirect URLs**
   - Add: `https://localhost:49251/callback`
5. Add **User Token Scopes**:

| Scope | Purpose |
|-------|---------|
| `channels:read`, `channels:history` | Read public channels |
| `groups:read`, `groups:history` | Read private channels |
| `im:read`, `im:history` | Read DMs |
| `mpim:read`, `mpim:history` | Read group DMs |
| `users:read`, `users:read.email` | User lookup |
| `search:read` | Fast sync (--my-channels) |
| `chat:write` | Send messages |
| `files:read`, `files:write` | File upload |

6. **Install to Workspace** and copy **Client ID** and **Client Secret**

### 2. Set Environment Variables

```bash
export SLACLI_CLIENT_ID="your-client-id"
export SLACLI_CLIENT_SECRET="your-client-secret"
```

Add to `~/.bashrc`, `~/.zshrc`, or equivalent.

### 3. Authenticate

```bash
slacli auth
```

Opens browser for OAuth. Token stored securely in `~/.slacli/credentials.json` (chmod 600).

### 4. Initial Sync

```bash
# Fast: Only channels you've posted in (recommended for first sync)
slacli sync --my-channels

# Full: All channels you have access to
slacli sync
```

## Command Reference

### Authentication

```bash
slacli auth              # Start OAuth flow
slacli auth --status     # Check auth status
slacli auth --refresh    # Force token refresh
slacli auth --logout     # Clear credentials
```

### Sync

```bash
slacli sync                       # Incremental sync
slacli sync --full                # Full re-sync (reset cursors)
slacli sync --my-channels         # Fast: only channels you've posted in
slacli sync --recent --days 7     # Incrementally sync recently active channels
slacli sync --days 60             # Sync 60 days of history
slacli sync --active-days 7       # Fast: active channel messages
slacli sync --channels-only       # Sync channel metadata only
slacli sync --follow              # Continuous sync (30s intervals)
slacli sync --threads             # Fill missing thread replies
slacli sync --threads --full      # Force re-sync all known thread replies
slacli sync --threads --active-days 7  # Fill recent thread replies
```

### Upgrade

```bash
slacli upgrade                    # Replace current install from latest GitHub release
slacli upgrade --ref v0.3.0       # Install a tag release, or build from source if needed
slacli upgrade --ref main         # Build and install the latest main checkout
```

### Channels

```bash
slacli channels list                        # All channels
slacli channels list --sort last_received  # By most recent message
slacli channels list --sort last_sent      # By your last message
slacli channels list --sort last_mention   # By last @mention
slacli channels list --type dm             # Only DMs
slacli channels list --type channel        # Only channels
slacli channels list --unread              # With unread messages
slacli channels list --limit 10            # Top 10
slacli channels list --json                # JSON output
```

### Search

```bash
slacli search "deployment"             # Hybrid local + live search
slacli search --local "deployment"     # Local index only, no network
slacli search --live "deployment"      # Slack API only
slacli search "bug" --channel "#eng"   # Scope to channel
slacli search "review" --from alice@example.com
```

Search never runs inline sync. If the local index is stale, run:

```bash
slacli sync --recent --days 7
```

### Messages

**List:**
```bash
slacli messages list --channel "#general"                 # Channel history
slacli messages list --channel "#general" --limit 50      # Last 50
slacli messages list --channel "#general" --before 1704540600.123456
slacli messages list --channel "#general" --after 1704540600.123456
slacli messages list --channel "#general" --thread 1704540600.123456  # Thread
```

**Search:**
```bash
slacli messages search "deployment"                       # Hybrid local + live search
slacli messages search --local "deployment"               # Local index only
slacli messages search --live "deployment"                # Slack API only
slacli messages search "bug" --channel "#engineering"    # In channel
slacli messages search "review" --from alice@company.com  # From user
slacli messages search "api" --after 2024-01-01          # After date
slacli messages search "release" --before 2024-06-01     # Before date
slacli messages search "error" --limit 100               # Limit results
slacli messages search "query" --json                    # JSON output
```

**Context:**
```bash
slacli messages context <message_id>              # Messages around target
slacli messages context <message_id> --before 5   # 5 before
slacli messages context <message_id> --after 10   # 10 after
```

### Mentions

```bash
slacli mentions                       # All @mentions of you
slacli mentions --unread              # Unread only
slacli mentions --channel "#general"  # In specific channel
slacli mentions --since 2024-01-01    # Since date
slacli mentions --limit 20            # Limit results
slacli mentions --json                # JSON output
```

### Send

```bash
# To channel (by name)
slacli send "#random" "Hello team!"

# To DM (by email)
slacli send "alice@company.com" "Quick question"

# With file attachment
slacli send "#general" "Check this out" --file ./screenshot.png

# Reply to thread
slacli send "#general" "Thread reply" --thread 1704540600.123456

# From stdin (for pipes)
echo "Build succeeded" | slacli send "#ci" --stdin
cat report.txt | slacli send "boss@company.com" --stdin
```

### Drafts

Use `draft` when you want to compose a native Slack draft without sending it:

```bash
slacli draft "#engineering" "RFC: New auth system"
echo "Thread reply" | slacli draft "#eng" --thread 123.456 --stdin
```

The broader `drafts` command supports two modes:
1. **Real Drafts (xoxc)** — Syncs with Slack's native drafts (requires xoxc token setup)
2. **Scheduled Messages** — Fallback mode using scheduled messages (90 days out)

**Setup real drafts (recommended):**
```bash
# Configure xoxc credentials (interactive)
slacli drafts setup

# Or with flags
slacli drafts setup --token "xoxc-..." --cookie "xoxd-..." --workspace "finn"

# Check status
slacli drafts status
```

To get xoxc credentials:
1. Open Slack in browser → DevTools (F12)
2. **Token:** Application → Local Storage → `localConfig_v2` → find `xoxc-*` token
3. **Cookie:** Application → Cookies → app.slack.com → copy `d` cookie value

**Commands:**
```bash
slacli draft "#engineering" "RFC: New auth system"     # Create native Slack draft
slacli draft "#eng" --thread 123.456 --stdin           # Create draft from stdin
slacli drafts list                                    # List all drafts
slacli drafts list --channel "#engineering"           # Filter by channel
slacli drafts list --json                             # JSON output
slacli drafts create --channel "#engineering" --text "RFC: New auth system"
slacli drafts create --channel "#eng" --thread 123.456 --text "Thread reply"
slacli drafts show <draft-id>                         # View draft content
slacli drafts edit <draft-id> --text "Updated text"   # Edit draft (xoxc only)
slacli drafts send <draft-id>                         # Send draft immediately
slacli drafts delete <draft-id>                       # Delete draft
```

### Users

```bash
slacli users list                    # All users
slacli users list --search "alice"   # Search by name/email
slacli users list --json             # JSON output
```

### Database

```bash
slacli db stats                              # DB size, message count, date range
slacli db prune --older-than 90 --dry-run    # Preview deletion
slacli db prune --older-than 90 --force      # Delete messages >90 days old
slacli db vacuum                             # Reclaim disk space
slacli db reset --force                      # Delete all data
```

### Config

```bash
slacli config get sync_days           # Get config value
slacli config set sync_days 60        # Set config value
slacli config whitelist add C123ABC   # Add channel to whitelist
slacli config whitelist remove C123   # Remove from whitelist
slacli config whitelist list          # Show whitelist
```

### Diagnostics

```bash
slacli doctor    # Check auth, DB, config, connectivity
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--json` | JSON output (for agents/scripts) |
| `--plain` | Plain text output (TSV) |
| `--quiet` | Suppress non-essential output |
| `--verbose` | Verbose logging |
| `--no-color` | Disable colored output |
| `--config` | Config file path |
| `--store` | Data directory path |

## Configuration

**File:** `~/.slacli/config.json`

```json
{
  "sync_days": 30,
  "retention_days": 90,
  "auto_prune": false,
  "whitelist": ["C123ABC", "C456DEF"]
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `sync_days` | 30 | Days of message history to sync |
| `retention_days` | 90 | Auto-prune threshold (if enabled) |
| `auto_prune` | false | Auto-delete old messages after sync |
| `whitelist` | [] | Channel IDs to always sync |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `SLACLI_CLIENT_ID` | Slack OAuth Client ID (required) |
| `SLACLI_CLIENT_SECRET` | Slack OAuth Client Secret (required) |
| `SLACLI_STORE` | Data directory (default: `~/.slacli`) |
| `SLACLI_TOKEN` | Direct token (skip OAuth flow) |
| `NO_COLOR` | Disable colored output |

## Data Storage

All data in `~/.slacli/` (or `$SLACLI_STORE`):

| File | Purpose | Permissions |
|------|---------|-------------|
| `credentials.json` | OAuth access/refresh tokens | 600 (user-only) |
| `slacli.db` | SQLite database with FTS5 | 644 |
| `config.json` | User configuration | 644 |
| `sync_state.json` | Sync cursors and metadata | 644 |

## Database Schema

```sql
-- Channels (public, private, DM, group DM)
channels (id, name, type, is_private, is_archived,
          last_message_ts, last_sent_ts, last_mention_ts,
          last_activity, unread_count, members, created_at, updated_at)

-- Users
users (id, email, name, display_name, avatar_url, is_bot, created_at, updated_at)

-- Messages
messages (id, channel_id, author_id, author_email, author_name, text,
          timestamp, thread_ts, reply_count, reactions, edited, created_at)

-- FTS5 virtual table (auto-synced via triggers)
messages_fts (id, channel_id, author_email, author_name, text, timestamp)
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Invalid usage |
| 3 | Authentication required |
| 4 | Network error |
| 5 | Not found |

## Shell Completion

```bash
# Bash
slacli completion bash > /etc/bash_completion.d/slacli

# Zsh
slacli completion zsh > "${fpath[1]}/_slacli"

# Fish
slacli completion fish > ~/.config/fish/completions/slacli.fish
```

## Agent Integration

slacli is designed for LLM/agent consumption:

```bash
# JSON output for parsing
slacli channels list --json | jq '.[] | select(.unread_count > 0)'
slacli messages search "error" --json | jq '.[0:5]'

# Pipe to send
echo "Task completed" | slacli send "#status" --stdin

# Script integration
CHANNEL_ID=$(slacli channels list --json | jq -r '.[] | select(.name=="general") | .id')
```

## Troubleshooting

**OAuth fails:**
- Ensure redirect URL is exactly `https://localhost:49251/callback`
- Check that port 49251 is not in use
- Verify CLIENT_ID and CLIENT_SECRET are set

**Sync is slow:**
- Use `--my-channels` for initial sync (much faster)
- Add frequently used channels to whitelist
- Reduce `--days` for less history

**Search returns nothing:**
- Run `slacli sync` to populate database
- Check `slacli db stats` for message count
- Use `slacli doctor` to diagnose issues

**Token expired:**
- Run `slacli auth --refresh` or re-authenticate with `slacli auth`

## Development

```bash
# Clone
git clone https://github.com/maximilianwuehr/slacli.git
cd slacli

# Build
go build -o slack ./cmd/slack

# Test
go test ./...

# Lint
golangci-lint run
```

## License

MIT
