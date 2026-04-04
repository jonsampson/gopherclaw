# gopherclaw

Go reimplementation of [openclaw](https://github.com/openclaw/openclaw) — a personal AI assistant platform that connects messaging channels to OpenCode agents running in isolated containers.

See [README.md](README.md) for user-facing documentation. See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines and the skill-branch model.

## Quick Context

Single Go binary. Channel adapters (WhatsApp, Telegram, Slack, Discord) are skills that self-register at startup based on present credentials. Messages route through an allowlist and per-group queue to an OpenCode agent subprocess. Each group has an isolated folder (`groups/<name>/`) and its own `instructions.md` system prompt. SQLite persists all state.

## Key Files

| File | Purpose |
|------|---------|
| `cmd/gopherclaw/main.go` | Entry point: wires config, db, allowlist, queue, scheduler, channel adapters |
| `internal/types/types.go` | Core data structures and interfaces (no internal imports) |
| `internal/db/db.go` | SQLite persistence (messages, chats, groups, sessions, tasks) |
| `internal/allowlist/allowlist.go` | Sender allowlist: wildcard and per-JID rules, drop vs trigger mode |
| `internal/routing/routing.go` | JID classification and group snapshot for agent context |
| `internal/queue/queue.go` | Per-group serialised queue with global concurrency cap and idle preemption |
| `internal/scheduler/scheduler.go` | Drift-free next-run computation and `StartSchedulerLoop` |
| `internal/runner/runner.go` | Spawns agent subprocess, captures output between sentinel markers |
| `groups/main/instructions.md` | Default main-group agent system prompt (edit this to customise the agent) |
| `groups/global/` | Shared resources accessible to all groups |
| `container/Dockerfile` | Agent container image: debian-slim + opencode CLI, reads JSON from stdin |
| `container/build.sh` | Builds the agent container image |
| `container/entrypoint.sh` | Container entrypoint: runs opencode, emits sentinel-wrapped output with SESSION_ID |
| `container/skills/` | Container-side skills loaded inside the agent container at runtime |
| `launchd/com.gopherclaw.plist` | macOS launchd service file (template vars substituted by `/setup`) |
| `systemd/gopherclaw.service` | Linux systemd user service file (template vars substituted by `/setup`) |

## Secrets / Credentials / OneCLI

API keys, secret keys, OAuth tokens, and auth credentials are managed by the OneCLI gateway — which handles secret injection into containers at request time, so no keys or tokens are ever passed to containers directly. Run `onecli --help`. See `/init-onecli` to install.

## Skills

gopherclaw uses the same skill model as openclaw. Skills are the primary extension mechanism — not PRs.

| Type | Location | How to apply |
|------|----------|--------------|
| Feature skill | `skill/*` branch | `git merge skill/<name>` |
| Utility skill | `.opencode/skills/<name>/` | Run the skill directly |
| Operational skill | `.opencode/skills/<name>/` | Instruction-only, always on `main` |
| Container skill | `container/skills/<name>/` | Loaded inside the agent container |

Common skills:
- `/add-whatsapp` — Add WhatsApp channel adapter
- `/add-telegram` — Add Telegram channel adapter
- `/add-slack` — Add Slack channel adapter
- `/add-discord` — Add Discord channel adapter

## Development

Run commands directly — don't tell the user to run them.

```bash
# Build
go build -o gopherclaw ./cmd/gopherclaw

# Test (fast)
go test ./...

# Test (full, matches CI)
go test -race -count=1 -timeout=120s ./...

# Vet
go vet ./...

# Lint (requires golangci-lint)
golangci-lint run

# Format
gofmt -l -w .

# Build agent container
./container/build.sh
```

Service management:
```bash
# macOS (launchd) — substitute {{...}} vars in plist first via /setup
launchctl load ~/Library/LaunchAgents/com.gopherclaw.plist
launchctl unload ~/Library/LaunchAgents/com.gopherclaw.plist
launchctl kickstart -k gui/$(id -u)/com.gopherclaw  # restart

# Linux (systemd) — substitute {{...}} vars in service file first via /setup
systemctl --user start gopherclaw
systemctl --user stop gopherclaw
systemctl --user restart gopherclaw
```

## Package Import Rules

- `internal/types` has no internal imports — all packages may import it.
- `internal/scheduler` depends only on `internal/types`; never imports `db` or `queue`.
- `internal/runner` is stateless; it knows nothing about groups, queues, or messages.
- No circular dependencies — `go build ./...` catches cycles at compile time.

## Contributing

Before opening a PR, read [CONTRIBUTING.md](CONTRIBUTING.md).

**One rule:** Don't add features. Add skills.

Core PRs are accepted only for bug fixes, security fixes, and improvements that benefit every user. New channels, new agent capabilities, and opinionated behaviour belong in `skill/*` branches, not in core code.
