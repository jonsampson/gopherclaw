# Contributing to gopherclaw

## The core philosophy

> **Don't add features. Add skills.**

gopherclaw is designed to be forked by each user and customised directly in
their fork. The base codebase is intentionally small — small enough that Claude
can safely read and modify any part of it. Features that only some users need
should live on `skill/*` branches that users opt into by merging, not in core
code that everyone carries whether they want it or not.

### What belongs in core

| Accepted | Not accepted |
|---|---|
| Security fixes | New messaging-platform adapters |
| Bug fixes | New agent capabilities or tools |
| Performance improvements | Opinionated default behaviour |
| Correctness fixes (e.g. scheduler, queue) | UI or dashboard code |
| Documentation improvements | Anything that benefits fewer than ~90% of users |

If you are unsure, open an issue first to discuss before building.

---

## Skills

A **skill** is a named `skill/*` git branch. Users apply it with:

```sh
git merge skill/your-feature-name
```

Each skill branch contains:
- The code changes for the feature.
- A `SKILL.md` file at the repo root (≤ 500 lines) describing:
  - What the skill does.
  - Prerequisites (API keys, credentials, system dependencies).
  - How to apply and configure it.
  - What files it modifies.

### Submitting a skill

1. Fork the repo and create your feature branch from `main`:
   ```sh
   git checkout -b skill/channel-telegram
   ```
2. Build the feature; keep changes focused and minimal.
3. Add `SKILL.md` at the repo root.
4. Open a PR targeting `main`. The title should begin with `skill:`, e.g.
   `skill: add Telegram channel adapter`.
5. If accepted, maintainers merge the branch under `skill/channel-telegram` so
   users can apply it with `git merge skill/channel-telegram`.

---

## Dev setup

### Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Go | 1.20 or later | Module targets Go 1.25; earlier toolchains work for development. |
| `gcc` (or compatible C compiler) | Any recent version | Required by `go-sqlite3` (CGO). On macOS: Xcode CLI tools. On Debian/Ubuntu: `build-essential`. On Fedora/RHEL: `gcc`. |
| golangci-lint | Latest stable | Required for the lint step (see [Linting](#linting)). |

### First-time setup

```sh
git clone https://github.com/jonsampson/gopherclaw.git
cd gopherclaw
go mod download
go build ./...
```

No code generation, no external services needed for core package development.

---

## Running tests

```sh
# Quick check
go test ./...

# Full check — matches CI (use this before opening a PR)
go test -race -count=1 -timeout=120s ./...
```

The `db` package provides `db.InitTestDB()`, which opens an in-memory SQLite
database. All database tests use this helper — no disk fixtures to set up.

When writing tests that involve the scheduler, pass explicit `time.Time` values
rather than `time.Now()` so tests are deterministic. Use the `scheduler.RunOnce`
test helper (exported via `internal/scheduler/export_test.go`) to drive
scheduler ticks with a fake clock.

---

## Linting

```sh
# Install (Linux/macOS)
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
  | sh -s -- -b $(go env GOPATH)/bin latest

# Or via Homebrew
brew install golangci-lint

# Run
golangci-lint run
```

All lint findings must be resolved before a PR can be merged. If a suppression
is genuinely needed, add `//nolint:<linter> // reason` with a brief explanation.

---

## Code style

**Formatting.** All Go files must be formatted with `gofmt`. Run:

```sh
gofmt -l -w .
```

No file should appear in `gofmt -l .` output when a PR is opened.

**Godoc comments.** Every exported symbol must have a godoc comment that:
- Begins with the name of the symbol.
- Ends with a period.
- Fits on one line for the summary sentence.

```go
// GroupQueue manages concurrent agent execution with per-group serialisation
// and a global concurrency limit.
type GroupQueue struct { … }
```

**Table-driven tests.** Prefer table-driven tests over independent test
functions when exercising multiple input/output combinations:

```go
tests := []struct {
    name string
    in   string
    want bool
}{
    {"wildcard allows everyone", "*", true},
    {"empty list blocks all",    "",  false},
}
for _, tc := range tests {
    t.Run(tc.name, func(t *testing.T) { … })
}
```

**Error wrapping.** Use `fmt.Errorf("context: %w", err)` so callers can use
`errors.Is` / `errors.As`. Never silently discard errors; if intentional, add
a comment explaining why.

**Logging.** Use the standard `log` package. Do not introduce a structured
logger without prior discussion.

---

## Package layout and import rules

```
cmd/
  gopherclaw/        Entry point; wires all internal packages together.
internal/
  types/             Core data structures and interfaces.
  db/                SQLite persistence layer.
  allowlist/         Sender access control.
  routing/           Chat JID classification and group resolution.
  queue/             Concurrency-controlled work queue.
  scheduler/         Next-run computation and scheduler loop.
  runner/            Agent subprocess execution and output parsing.
groups/
  main/
    AGENTS.md        Agent system prompt for the main group (edit this).
    conversations/   History written by the agent.
```

Import rules:

- **`internal/types` has no internal imports.** Every other package may import
  it; it must not import any sibling.
- **No circular dependencies.** `go build ./...` catches cycles at compile time.
- **`internal/scheduler` uses interfaces, not concrete types.** It must not
  import `internal/db` or `internal/queue`. The loop wiring lives in
  `cmd/gopherclaw/main.go`.
- **`internal/runner` is stateless.** It accepts a `ContainerInput` and returns
  a `ContainerOutput`; it knows nothing about groups, queues, or messages.
- **`internal/` is module-private** and cannot be imported from outside the
  module. Public extension points are `types.Channel` and the callback types.

---

## How to add a channel adapter as a skill

Channel adapters are the most common type of skill. Follow these steps:

### 1. Create the skill branch

```sh
git checkout -b skill/channel-telegram
```

### 2. Implement `types.Channel`

Create `internal/channels/telegram/telegram.go`:

```go
package telegram

import "github.com/jonsampson/gopherclaw/internal/types"

type Adapter struct {
    onMessage  types.OnInboundMessage
    onMetadata types.OnChatMetadata
    // … platform client fields
}

func New(onMessage types.OnInboundMessage, onMetadata types.OnChatMetadata) *Adapter {
    return &Adapter{onMessage: onMessage, onMetadata: onMetadata}
}

func (a *Adapter) Connect() error                { … }
func (a *Adapter) Disconnect() error             { … }
func (a *Adapter) SendMessage(jid, text string) error { … }
```

Call `a.onMessage(chatJID, msg)` for each inbound message and
`a.onMetadata(chatJID, name, isGroup, a)` when group metadata is discovered.

### 3. Self-register based on credentials

In an `init()` function or explicit `Register()` call, check whether the
required credentials are present and wire the adapter into main only if they
are. This keeps the binary functional even without Telegram configured:

```go
// internal/channels/telegram/register.go
func init() {
    if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
        channels.Register("telegram", func(onMsg types.OnInboundMessage, onMeta types.OnChatMetadata) types.Channel {
            return New(onMsg, onMeta)
        })
    }
}
```

### 4. Wire into `cmd/gopherclaw/main.go`

Import the package for its side effect:

```go
import _ "github.com/jonsampson/gopherclaw/internal/channels/telegram"
```

Then iterate over registered channel factories and connect each one.

### 5. Add `SKILL.md`

At the repo root, add `SKILL.md` describing prerequisites (`TELEGRAM_BOT_TOKEN`
env var, BotFather setup, webhook vs polling mode) and how to apply the skill.

### 6. Add tests

Write unit tests for `Connect`, `Disconnect`, and `SendMessage` using a mock
HTTP server or in-memory stub. Cover edge cases like network errors, unknown
JID formats, and rate limiting.

### 7. Submit

Open a PR with title `skill: add Telegram channel adapter`. If accepted, it
merges as `skill/channel-telegram`.

---

## PR checklist

Before requesting review, confirm all of the following:

- [ ] `go test -race -count=1 -timeout=120s ./...` passes with no failures.
- [ ] `go vet ./...` reports no issues.
- [ ] `golangci-lint run` reports no issues (or suppressions are documented).
- [ ] `gofmt -l .` produces no output.
- [ ] All new exported symbols have godoc comments ending in a period.
- [ ] New behaviour is covered by tests (table-driven where appropriate).
- [ ] `README.md` is updated if the change affects user-visible behaviour,
  configuration, or the public interface.
- [ ] This change belongs in core (not in a skill branch). If unsure, discuss
  in an issue before building.
- [ ] No new external dependencies have been introduced without prior discussion
  in a GitHub issue.
- [ ] The PR description explains *why* the change is needed, not just *what*
  it does.

---

## Commit message style

Use the imperative mood, as if completing "If applied, this commit will …":

```
Add Telegram channel adapter skill
Fix interval scheduler drift when tasks are missed
Document GOPHERCLAW_CLOSE_DIR in README
```

Guidelines:

- **Subject line:** 72 characters or fewer. No trailing period.
- **Body (optional):** Separated from the subject by a blank line. Explain the
  motivation and non-obvious design decisions. Wrap at 80 characters.
- **Scope prefix (optional):** You may prefix with the package name in
  lowercase: `queue: deduplicate task items by TaskID`.
- **One logical change per commit.** Bug fix and unrelated feature → two commits.
