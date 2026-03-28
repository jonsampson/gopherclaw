---
name: add-matrix
description: Add a Matrix homeserver channel adapter to gopherclaw.
---

## Prerequisites

- A Matrix account for the bot (e.g. `@gopherclaw:yourserver.com`)
- Bot access token — obtain via Element → Settings → Help & About → Access Token,
  or via the login API:
  ```bash
  curl -XPOST 'https://yourserver.com/_matrix/client/v3/login' \
    -H 'Content-Type: application/json' \
    -d '{"type":"m.login.password","user":"gopherclaw","password":"yourpassword"}'
  ```
- Invite the bot account to any rooms you want it to join

## Apply

```bash
git merge skill/matrix
```

## Configure

Set these environment variables before running gopherclaw:

| Variable | Example | Description |
|----------|---------|-------------|
| `MATRIX_HOMESERVER` | `https://matrix.org` | Homeserver URL |
| `MATRIX_USER_ID` | `@bot:matrix.org` | Full Matrix user ID |
| `MATRIX_ACCESS_TOKEN` | `syt_...` | Bot access token |

## Register a room

After starting gopherclaw, register the Matrix room in the database so the bot
knows which group folder to use:

```bash
sqlite3 gopherclaw.db "
INSERT INTO registered_groups (jid, name, folder, trigger, is_main)
VALUES ('!roomid:yourserver.com', 'My Room', 'groups/myroom', '@Claw', 0);
"
```

Then create the group folder and system prompt:

```bash
mkdir -p groups/myroom/conversations
cp groups/main/CLAUDE.md groups/myroom/CLAUDE.md
# Edit groups/myroom/CLAUDE.md to customise the agent for this room
```

## Usage

In the registered Matrix room, prefix messages with the trigger word:

```
@Claw what's the weather today?
```

The main group (is_main=1) responds to all messages without a trigger prefix.

## What this skill changes

- `internal/channels/` — channel adapter registry (new package)
- `internal/channels/matrix/` — Matrix adapter using mautrix-go (new package)
- `internal/queue/queue.go` — adds `ChatJID` field to `Item` for correct reply routing
- `cmd/gopherclaw/main.go` — inbound message routing via `onMessage`/`onMetadata` callbacks;
  channels registry replaces hardcoded noopChannel
- `go.mod` / `go.sum` — adds `maunium.net/go/mautrix` dependency
