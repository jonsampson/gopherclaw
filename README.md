# gopherclaw

[![CI](https://github.com/jonsampson/gopherclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/jonsampson/gopherclaw/actions/workflows/ci.yml)

**gopherclaw** is a Go reimplementation of [openclaw / nanoclaw](https://github.com/openclaw/openclaw) — a personal AI assistant platform that connects messaging channels (WhatsApp, Telegram, Slack, Discord, and others) to Claude AI agents running on your own hardware.

> **Fork-first.** This repository is designed to be forked, not just imported.
> Clone it, make it yours, and run it. Customisation happens in your fork —
> not through configuration files, and not through PRs to this repo.
> See [Contributing](#contributing) for the distinction between core changes
> and skills.

---

## How it works

A message travels through the following pipeline:

```
Messaging channel
       │
       ▼
  Channel adapter      implement types.Channel; call OnInboundMessage
       │
       ▼
  Allowlist check      internal/allowlist
  ┌─────────────────────────────────────────────────────────┐
  │ mode=drop    → message silently discarded               │
  │ mode=trigger → message stored, not forwarded to agent   │
  │ allowed      → continue                                 │
  └─────────────────────────────────────────────────────────┘
       │
       ▼
  Routing              internal/routing
  Match chat JID → RegisteredGroup; non-main groups require
  the message to begin with the group's trigger prefix.
       │
       ▼
  Queue                internal/queue
  ┌─────────────────────────────────────────────────────────┐
  │ Per-group serialisation: one agent run at a time.       │
  │ Global concurrency cap across all groups.               │
  │ Scheduled tasks are prioritised over messages.          │
  └─────────────────────────────────────────────────────────┘
       │
       ▼
  Agent script         internal/runner
  Execute /bin/sh script in the group's folder.
  The Claude CLI reads groups/<name>/CLAUDE.md as context.
  Capture output between the structured markers.
       │
       ▼
  Response             types.Channel.SendMessage
  Deliver captured output back to the originating chat.
       │
       ▼
  SQLite               internal/db
  Messages, chat metadata, sessions, tasks, and router state
  are persisted throughout the flow.
```

The **scheduler** (`internal/scheduler`) runs alongside the message loop,
polling the database for tasks whose `next_run` timestamp has passed and
enqueuing them as high-priority items.

---

## Quick start

### Prerequisites

| Requirement | Notes |
|---|---|
| Go 1.20 or later | Tested with Go 1.25 |
| `gcc` (or a compatible C compiler) | Required by `go-sqlite3` (CGO) |
| SQLite3 development headers | `libsqlite3-dev` on Debian/Ubuntu; `sqlite-devel` on Fedora/RHEL; included in Xcode CLI tools on macOS |
| `claude` CLI | Install from [claude.ai/code](https://claude.ai/code) and authenticate with `claude login` |

### Clone and build

```sh
git clone https://github.com/jonsampson/gopherclaw.git
cd gopherclaw
go build -o gopherclaw ./cmd/gopherclaw
```

### Customise your agent

Edit `groups/main/CLAUDE.md` — this is your agent's system prompt and persistent
memory. Change the name, personality, and instructions to suit your use case.
The file is picked up automatically when the Claude CLI runs inside that folder.

### Run

```sh
export GOPHERCLAW_DB=gopherclaw.db
./gopherclaw
```

The process starts the scheduler loop and waits for a channel adapter to
deliver messages. By default it ships with a no-op channel stub; apply a skill
branch to add a real platform (see [Skills](#skills)).

---

## Groups and CLAUDE.md

Each registered group has a folder under `groups/`:

```
groups/
  main/
    CLAUDE.md          ← agent system prompt + memory (edit this)
    conversations/     ← searchable history written by the agent
  whatsapp_family/
    CLAUDE.md
    conversations/
  slack_work/
    CLAUDE.md
    …
```

**`CLAUDE.md`** is the single file that defines how your agent behaves in that
group. Edit it in plain Markdown — the Claude CLI reads it every time it runs.
It can contain:

- Personality and tone instructions
- A list of capabilities and tools
- Formatting rules (WhatsApp vs Slack vs Discord syntax)
- Persistent facts the agent should remember
- Instructions for managing tasks and groups

### Main group vs other groups

| | Main group | Other groups |
|---|---|---|
| Trigger | No trigger needed — every message is processed | Message must begin with `@Claw` (or the group's configured trigger) |
| Filesystem access | Read-only project root + read-write own folder | Read-write own folder only |
| Privileges | Can inspect all groups and manage tasks | Isolated to own folder |

---

## Skills

Skills are the primary way to add features to your gopherclaw deployment.
A skill is a named `skill/*` git branch — apply it with a single merge:

```sh
git merge skill/whatsapp   # add WhatsApp support
git merge skill/telegram   # add Telegram support
git merge skill/slack      # add Slack support
```

Each skill branch ships with a `SKILL.md` that describes what it does, what
credentials or configuration it needs, and how to apply it.

### Skill types

| Type | How to apply | Description |
|---|---|---|
| **Feature skill** | `git merge skill/<name>` | Adds code (e.g. a channel adapter) to your fork |
| **Utility skill** | Run the script directly | A standalone script with no code changes |
| **Container skill** | Copy into group folder | A tool available inside the agent's sandbox |

### Finding skills

Community skills are listed on [ClawHub](https://github.com/openclaw/clawhub).
You can also write your own — see [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Configuration

gopherclaw is configured via environment variables. All are optional; defaults
are shown below.

| Variable | Default | Description |
|---|---|---|
| `GOPHERCLAW_DB` | `gopherclaw.db` | Path to the SQLite database. Created automatically on first run. |
| `GOPHERCLAW_ALLOWLIST` | `allowlist.json` | Path to the JSON sender allowlist. Falls back to allow-all on missing or malformed file. |
| `GOPHERCLAW_CLOSE_DIR` | *(empty — disabled)* | Directory for per-group `_close` signal files used for idle preemption. Leave unset to disable. |
| `GOPHERCLAW_MAX_CONCURRENT` | `4` | Maximum simultaneous agent runs across all groups. |
| `GOPHERCLAW_AGENT_TIMEOUT` | `5m` | Maximum wall-clock time per agent run. Accepts Go duration strings (`120s`, `5m`). |
| `GOPHERCLAW_SCHEDULER_POLL` | `15s` | How often the scheduler checks for due tasks. |

---

## Allowlist config

The allowlist file is a JSON document that controls which senders may interact
with the bot. Missing or malformed files fall back to allow-all (trigger mode).

```json
{
  "allow": "*",
  "mode": "trigger",
  "log_denied": true,
  "per_chat": {
    "120363000000000001@g.us": {
      "allow": ["15551234567@s.whatsapp.net"],
      "mode": "drop"
    }
  }
}
```

| Field | Values | Description |
|---|---|---|
| `allow` | `"*"` or `["sender1", …]` | Who is permitted. `"*"` allows everyone. |
| `mode` | `"trigger"` / `"drop"` | `trigger`: unallowed messages are stored but not processed. `drop`: unallowed messages are discarded entirely. |
| `log_denied` | `true` / `false` | Log each denied sender (default `true`). |
| `per_chat` | object | Per-chat JID overrides; `mode` and `log_denied` inherit from the top level if omitted. |

---

## Agent scripts and output markers

When the queue dispatches an item, `cmd/gopherclaw/main.go` runs a shell script
in the group's folder. The script must print the agent's response between two
sentinel lines so the runner can capture it:

```sh
echo '---GOPHERCLAW_OUTPUT_START---'
claude --print "$(cat pending_message.txt)" 2>&1
echo '---GOPHERCLAW_OUTPUT_END---'
```

Everything printed outside the markers is ignored. Running the Claude CLI
inside `groups/<name>/` causes it to pick up `CLAUDE.md` automatically.

The placeholder `buildScript` in `cmd/gopherclaw/main.go` shows where to put
this invocation — replace it with the appropriate `claude` command for your
setup.

---

## Scheduled tasks

Tasks are rows in the `scheduled_tasks` table, managed via SQL or the agent
itself (the main group CLAUDE.md includes instructions for this).

| `schedule_type` | `schedule_value` | Behaviour |
|---|---|---|
| `once` | *(ignored)* | Runs once when `next_run ≤ now`, then status → `done`. |
| `interval` | Seconds as a string (e.g. `"3600"`) | Repeats every N seconds; missed intervals are skipped, not replayed. |
| `cron` | Five-field UTC cron expression (e.g. `"0 9 * * 1-5"`) | Repeats per cron schedule, evaluated in UTC. |

Scheduled tasks are enqueued as high-priority items and preempt queued messages.

---

## MCP tools

Agents run inside Docker containers with `groups/<name>/.claude/` mounted at
`/home/claude/.claude/`. This directory persists across runs (the container is
`--rm` but the host directory is not), so any MCP server configuration written
there survives between invocations.

### How gopherclaw handles MCP

gopherclaw does **not** act as an MCP server or broker MCP calls on behalf of
agents. Each agent configures and launches its own MCP servers directly, the
same way any Claude Code session would. This keeps gopherclaw's role narrow
(routing and scheduling) and lets each group's agent have a different set of
tools without any changes to gopherclaw itself.

### Configuring MCP servers

MCP servers are registered in `~/.claude/settings.json` (inside the container,
which maps to `groups/<name>/.claude/settings.json` on the host). Use
`claude mcp add` or edit the file directly.

The simplest way to pre-configure MCP for all runs of a group is to create the
settings file once on the host:

```sh
# On the host, before the first agent run for that group:
mkdir -p groups/my-group/.claude
claude mcp add --scope local my-tool -- /path/to/mcp-server
# The resulting settings.json is now at groups/my-group/.claude/settings.json
# and will be present inside the container on every run.
```

Alternatively, add a container skill that writes the configuration at container
startup — see `container/skills/README.md`.

### MCP servers that need network access or secrets

Because containers run with `--rm` and no network policy beyond the default
Docker bridge, MCP servers that call external APIs must either:

- Run on the **host** and be reachable from inside the container via the Docker
  host gateway (`host-gateway` extra host or `host.docker.internal`), or
- Be bundled **inside the container image** as part of a container skill.

Secrets (API keys) for MCP servers follow the same rule as all other secrets:
managed by the OneCLI gateway, injected at request time. See
[Secrets / Credentials / OneCLI](#secrets--credentials--onecli).

---

## Channel adapters

A channel adapter is any type that implements `types.Channel`:

```go
type Channel interface {
    Connect() error
    Disconnect() error
    SendMessage(chatJID, text string) error
}
```

When a message arrives, the adapter calls `types.OnInboundMessage`. When group
metadata is discovered (name, JID, is-group), it calls `types.OnChatMetadata`.

The base repo ships with a no-op stub. Apply a skill branch to add a real
platform, or implement `types.Channel` yourself and wire it into
`cmd/gopherclaw/main.go`. See [CONTRIBUTING.md](CONTRIBUTING.md) for the
self-registration pattern that skill-based adapters use.

---

## Package reference

| Package | Import path | Responsibility |
|---|---|---|
| `types` | `.../internal/types` | Core data structures and interfaces. No internal imports; all other packages may depend on it. |
| `db` | `.../internal/db` | SQLite persistence (messages, chats, groups, sessions, tasks, task logs, router state). |
| `allowlist` | `.../internal/allowlist` | Loads the JSON allowlist; provides `IsSenderAllowed`, `ShouldDropMessage`, `IsTriggerAllowed`. |
| `routing` | `.../internal/routing` | JID classification (group vs DM) and sorted group snapshots for agent context. |
| `queue` | `.../internal/queue` | Concurrency-controlled work queue with per-group serialisation, backoff retry, task dedup, and idle preemption. |
| `scheduler` | `.../internal/scheduler` | Drift-free next-run computation; `StartSchedulerLoop` for polling due tasks. |
| `runner` | `.../internal/runner` | Executes agent shell scripts, parses `OutputStart`/`OutputEnd` markers, enforces activity timeout. |

---

## Running tests

CGO is required (for `go-sqlite3`). The race detector is enabled in CI and
recommended locally before committing.

```sh
# Quick check
go test ./...

# Full check — matches CI
go test -race -count=1 -timeout=120s ./...
```

The `db` package exposes `db.InitTestDB()` for an in-memory SQLite database —
no temporary files needed in tests.

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full guide. The short version:

> **Don't add features. Add skills.**

Core PRs are accepted for bug fixes and improvements that benefit all users.
New channels, agent capabilities, and opinionated behaviour belong in skill
branches. This keeps the base code small enough for Claude to safely modify
in any user's fork.

---

## License

This project is licensed under the [MIT License](LICENSE).
