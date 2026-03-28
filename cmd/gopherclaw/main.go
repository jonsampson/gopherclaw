// Command gopherclaw is the runtime for the Go reimplementation of openclaw.
//
// It wires together the internal packages into a running agent gateway:
//  1. Loads configuration from environment variables.
//  2. Opens the SQLite database.
//  3. Loads the sender allowlist.
//  4. Creates the group queue (concurrency-controlled agent runs).
//  5. Starts the scheduler loop (interval / cron / once tasks).
//  6. Blocks until SIGTERM or SIGINT, then shuts down gracefully.
//
// Channel adapters (WhatsApp, Telegram, Slack, …) are not wired in the base
// repo. Add them by merging a skill branch, e.g. `git merge skill/whatsapp`.
// See CONTRIBUTING.md for how to build and submit skill branches.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jonsampson/gopherclaw/internal/allowlist"
	"github.com/jonsampson/gopherclaw/internal/db"
	"github.com/jonsampson/gopherclaw/internal/queue"
	"github.com/jonsampson/gopherclaw/internal/runner"
	"github.com/jonsampson/gopherclaw/internal/scheduler"
	"github.com/jonsampson/gopherclaw/internal/types"
)

// ---- Configuration ----------------------------------------------------------

type config struct {
	// DBPath is the path to the SQLite database file.
	// Env: GOPHERCLAW_DB (default: gopherclaw.db)
	DBPath string

	// AllowlistPath is the path to the JSON sender allowlist config.
	// Env: GOPHERCLAW_ALLOWLIST (default: allowlist.json)
	AllowlistPath string

	// CloseDir is the directory under which per-group _close signal files are
	// written to request idle-preemption. Leave empty to disable.
	// Env: GOPHERCLAW_CLOSE_DIR (default: "")
	CloseDir string

	// MaxConcurrent is the maximum number of simultaneously running agent
	// subprocesses across all groups.
	// Env: GOPHERCLAW_MAX_CONCURRENT (default: 4)
	MaxConcurrent int

	// AgentTimeout is the maximum wall-clock time for a single agent run.
	// Env: GOPHERCLAW_AGENT_TIMEOUT (default: 5m)
	AgentTimeout time.Duration

	// SchedulerPoll is how often the scheduler checks for due tasks.
	// Env: GOPHERCLAW_SCHEDULER_POLL (default: 15s)
	SchedulerPoll time.Duration
}

func loadConfig() (config, error) {
	cfg := config{
		DBPath:        envOr("GOPHERCLAW_DB", "gopherclaw.db"),
		AllowlistPath: envOr("GOPHERCLAW_ALLOWLIST", "allowlist.json"),
		CloseDir:      os.Getenv("GOPHERCLAW_CLOSE_DIR"),
		MaxConcurrent: 4,
		AgentTimeout:  5 * time.Minute,
		SchedulerPoll: 15 * time.Second,
	}

	if s := os.Getenv("GOPHERCLAW_MAX_CONCURRENT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("GOPHERCLAW_MAX_CONCURRENT: invalid positive integer %q", s)
		}
		cfg.MaxConcurrent = n
	}
	if s := os.Getenv("GOPHERCLAW_AGENT_TIMEOUT"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("GOPHERCLAW_AGENT_TIMEOUT: invalid duration %q", s)
		}
		cfg.AgentTimeout = d
	}
	if s := os.Getenv("GOPHERCLAW_SCHEDULER_POLL"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("GOPHERCLAW_SCHEDULER_POLL: invalid duration %q", s)
		}
		cfg.SchedulerPoll = d
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ---- Channel stub -----------------------------------------------------------

// noopChannel is a placeholder Channel that accepts calls but does nothing.
//
// TODO: Replace with a real messaging platform adapter. Skill branches provide
// ready-made adapters — apply one with, e.g., `git merge skill/whatsapp`.
// Alternatively, implement types.Channel directly and wire it in below.
type noopChannel struct{}

func (noopChannel) Connect() error                { return nil }
func (noopChannel) Disconnect() error             { return nil }
func (noopChannel) SendMessage(_, _ string) error { return nil }

// ---- Agent execution --------------------------------------------------------

// processGroup runs the agent script for the given queue item, updating the
// persisted session ID on success and delivering the result to the channel.
func processGroup(item queue.Item, database *db.DB, ch types.Channel, timeout time.Duration) error {
	sessionID, _ := database.GetSession(item.GroupID)

	out := runner.RunContainerAgent(
		types.ContainerInput{
			GroupFolder:     item.GroupID,
			ChatJID:         item.GroupID,
			SessionID:       sessionID,
			IsScheduledTask: item.IsTask,
			Script:          buildScript(item.GroupID, sessionID),
		},
		nil, // onOutput: set this to deliver streaming output if your channel supports it
		timeout,
	)

	// Persist the new session ID so the next run resumes the same conversation.
	if out.NewSessionID != "" && out.NewSessionID != sessionID {
		if err := database.SetSession(item.GroupID, out.NewSessionID); err != nil {
			log.Printf("processGroup: SetSession %s: %v", item.GroupID, err)
		}
	}

	if out.Status == types.ContainerStatusError {
		return fmt.Errorf("agent error for group %s: %s", item.GroupID, out.Error)
	}

	// Deliver the captured result to the chat (not for scheduled task runs,
	// which typically deliver output via the task's own send_message tool).
	if out.Result != nil && *out.Result != "" && !item.IsTask {
		if err := ch.SendMessage(item.GroupID, *out.Result); err != nil {
			log.Printf("processGroup: SendMessage %s: %v", item.GroupID, err)
		}
	}

	return nil
}

// buildScript returns the shell script executed for each agent run.
//
// The script must print the agent's response between the gopherclaw output
// markers so the runner can capture it:
//
//	echo '---GOPHERCLAW_OUTPUT_START---'
//	# … your agent invocation here …
//	echo '---GOPHERCLAW_OUTPUT_END---'
//
// TODO: Replace the placeholder below with a real invocation, for example:
//
//	cd <groupFolder> && claude --print "<prompt>" 2>&1
//
// When the Claude CLI runs inside the group folder it automatically picks up
// groups/<name>/CLAUDE.md as its memory/system-prompt file.
func buildScript(groupFolder, _ string) string {
	return fmt.Sprintf(`
cd %q || exit 1
echo '%s'
# TODO: invoke the Claude CLI here, e.g.:
#   claude --print "$(cat pending_message.txt)" 2>&1
# The CLI reads CLAUDE.md in the current directory as its system prompt.
echo '%s'
`, groupFolder, runner.OutputStart, runner.OutputEnd)
}

// ---- Entry point ------------------------------------------------------------

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("gopherclaw: config error: %v", err)
	}

	// Open (or create) the SQLite database.
	database, err := db.InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("gopherclaw: db.InitDB(%s): %v", cfg.DBPath, err)
	}
	defer database.Close()

	// Load sender allowlist; falls back to allow-all on missing/invalid file.
	al := allowlist.LoadAllowlist(cfg.AllowlistPath)
	_ = al // consumed by message routing once a channel adapter is wired in

	// Create the group queue.
	q := queue.New(cfg.MaxConcurrent, 2*time.Second, 0)
	if cfg.CloseDir != "" {
		q.SetCloseDir(cfg.CloseDir)
	}

	// Placeholder channel — replace with a real adapter (see above).
	var ch types.Channel = noopChannel{}
	if err := ch.Connect(); err != nil {
		log.Fatalf("gopherclaw: channel.Connect: %v", err)
	}
	defer ch.Disconnect() //nolint:errcheck

	// Derive a context that is cancelled on SIGTERM or SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run the task scheduler in the background.
	go scheduler.StartSchedulerLoop(ctx, database, func(task types.ScheduledTask) {
		item := queue.Item{
			GroupID: task.GroupFolder,
			TaskID:  strconv.FormatInt(task.ID, 10),
			IsTask:  true,
		}
		q.EnqueueTask(item, func(it queue.Item) error {
			return processGroup(it, database, ch, cfg.AgentTimeout)
		})
	}, cfg.SchedulerPoll)

	log.Printf("gopherclaw: started (db=%s, maxConcurrent=%d, schedulerPoll=%s)",
		cfg.DBPath, cfg.MaxConcurrent, cfg.SchedulerPoll)

	// Block until shutdown signal.
	<-ctx.Done()
	log.Println("gopherclaw: shutdown signal received, draining queue…")
	q.Shutdown()
	log.Println("gopherclaw: exited cleanly")
}
