# slacli

Agent-native Slack CLI with local full-text search, sync, and messaging.

## Features

- **Local FTS**: Full-text search over synced messages using SQLite FTS5
- **OAuth Auth**: Secure browser-based authentication with token refresh
- **Incremental Sync**: Efficient cursor-based sync for channels and messages
- **Agent-Friendly**: JSON output for LLM/agent consumption
- **Drafts**: Create, edit, and send Slack drafts
- **Multiple Channel Types**: Public, private, DMs, and group DMs

## Installation

### From Source

```bash
go install github.com/maximilianwuehr/slacli/cmd/slacli@latest
```

### From Binary

Download from [Releases](https://github.com/maximilianwuehr/slacli/releases).

## Setup

### 1. Create Slack App

1. Go to [api.slack.com/apps](https://api.slack.com/apps)
2. Create New App > From scratch
3. Add OAuth scopes under **User Token Scopes**:
   - `channels:history`, `channels:read`
   - `groups:history`, `groups:read`
   - `im:history`, `im:read`
   - `mpim:history`, `mpim:read`
   - `users:read`, `users:read.email`
   - `search:read`
   - `chat:write`
   - `files:read`, `files:write`
4. Install to Workspace
5. Copy Client ID and Client Secret

### 2. Set Environment Variables

```bash
export SLACLI_CLIENT_ID="your-client-id"
export SLACLI_CLIENT_SECRET="your-client-secret"
```

### 3. Authenticate

```bash
slacli auth
```

Opens browser for OAuth flow. Token stored in `~/.slacli/credentials.json`.

### 4. Initial Sync

```bash
slacli sync
```

## Usage

### Search Messages

```bash
# Search all messages
slacli messages search "deployment" --json

# Search in specific channel
slacli messages search "bug" --channel "#engineering"

# Search from specific person
slacli messages search "review" --from alice@company.com
```

### List Channels

```bash
# By most recent message
slacli channels list --sort last_received

# By last message you sent
slacli channels list --sort last_sent

# By last mention
slacli channels list --sort last_mention

# Only DMs
slacli channels list --type dm
```

### Read Messages

```bash
# Channel history
slacli messages list --channel "#general" --limit 20

# Thread replies
slacli messages list --channel "#general" --thread 1704540600.123456
```

### See Mentions

```bash
slacli mentions --unread --json
```

### Send Messages

```bash
# To channel
slacli send "#random" "Hello team!"

# To DM (by email)
slacli send "alice@company.com" "Quick question"

# With file
slacli send "#general" "Check this out" --file ./screenshot.png

# From stdin
echo "Deployment complete" | slacli send "#ops" --stdin
```

### Drafts

```bash
# Create draft
slacli drafts create --channel "#engineering" --text "RFC: New auth system"

# List drafts
slacli drafts list --json

# Send draft
slacli drafts send <draft-id>
```

### Database Management

```bash
# Statistics
slacli db stats

# Delete old messages
slacli db prune --older-than 90 --dry-run
slacli db prune --older-than 90 --force

# Reclaim space
slacli db vacuum

# Full reset
slacli db reset --force
```

### Diagnostics

```bash
slacli doctor
```

## Configuration

Config file: `~/.slacli/config.json`

```json
{
  "sync_days": 30,
  "retention_days": 90,
  "auto_prune": false
}
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `SLACLI_CLIENT_ID` | Slack OAuth Client ID |
| `SLACLI_CLIENT_SECRET` | Slack OAuth Client Secret |
| `SLACLI_STORE` | Data directory (default: `~/.slacli`) |
| `SLACLI_TOKEN` | Direct token (skip OAuth) |
| `NO_COLOR` | Disable colored output |

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Invalid usage |
| 3 | Auth required |
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

## Data Storage

All data stored in `~/.slacli/`:

- `credentials.json` - OAuth tokens (chmod 600)
- `slacli.db` - SQLite database with FTS5
- `config.json` - User configuration
- `sync_state.json` - Sync cursors

## License

MIT
