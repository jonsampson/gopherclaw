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
	"strings"
	"syscall"
	"time"

	"github.com/jonsampson/gopherclaw/internal/allowlist"
	"github.com/jonsampson/gopherclaw/internal/channels"
	"github.com/jonsampson/gopherclaw/internal/db"
	"github.com/jonsampson/gopherclaw/internal/queue"
	"github.com/jonsampson/gopherclaw/internal/runner"
	"github.com/jonsampson/gopherclaw/internal/scheduler"
	"github.com/jonsampson/gopherclaw/internal/types"

	// Skill-branch channel adapters self-register via init() when their
	// environment variables are present. Add adapters by merging skill branches.
	_ "github.com/jonsampson/gopherclaw/internal/channels/matrix"
)

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

	// ContainerImage is the Docker image used for agent runs.
	// Env: GOPHERCLAW_CONTAINER_IMAGE (default: gopherclaw-agent:latest)
	ContainerImage string
}

func loadConfig() (config, error) {
	cfg := config{
		DBPath:         envOr("GOPHERCLAW_DB", "gopherclaw.db"),
		AllowlistPath:  envOr("GOPHERCLAW_ALLOWLIST", "allowlist.json"),
		CloseDir:       os.Getenv("GOPHERCLAW_CLOSE_DIR"),
		MaxConcurrent:  4,
		AgentTimeout:   5 * time.Minute,
		SchedulerPoll:  15 * time.Second,
		ContainerImage: envOr("GOPHERCLAW_CONTAINER_IMAGE", "gopherclaw-agent:latest"),
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

// noopChannel is a placeholder Channel that accepts calls but does nothing.
//
// TODO: Replace with a real messaging platform adapter. Skill branches provide
// ready-made adapters — apply one with, e.g., `git merge skill/whatsapp`.
// Alternatively, implement types.Channel directly and wire it in below.
type noopChannel struct{}

func (noopChannel) Connect() error                { return nil }
func (noopChannel) Disconnect() error             { return nil }
func (noopChannel) SendMessage(_, _ string) error { return nil }

// chatJID returns the chat JID for reply routing.
// It falls back to GroupID for scheduler tasks that don't set ChatJID.
func chatJID(item queue.Item) string {
	if item.ChatJID != "" {
		return item.ChatJID
	}
	return item.GroupID
}

// processGroup runs the agent script for the given queue item, updating the
// persisted session ID on success and delivering the result via sender.
func processGroup(item queue.Item, database *db.DB, sender types.Sender, timeout time.Duration) error {
	sessionID, _ := database.GetSession(item.GroupID)
	jid := chatJID(item)

	out := runner.RunContainerAgent(
		types.ContainerInput{
			GroupFolder:     item.GroupID,
			ChatJID:         jid,
			SessionID:       sessionID,
			IsScheduledTask: item.IsTask,
			Script:          buildScript(item.GroupID, sessionID),
		},
		nil, // onOutput: set this to deliver streaming output if your channel supports it
		timeout,
	)

	// The container entrypoint prefixes the output with "SESSION_ID:<id>\n".
	// Extract it here so the session can be persisted and the raw text
	// delivered to the channel is clean.
	newSessionID := sessionID
	if out.Result != nil {
		if line, rest, found := strings.Cut(*out.Result, "\n"); found && strings.HasPrefix(line, "SESSION_ID:") {
			newSessionID = strings.TrimPrefix(line, "SESSION_ID:")
			*out.Result = rest
		}
	}

	// Persist the session ID so the next run resumes the same conversation.
	if newSessionID != "" && newSessionID != sessionID {
		if err := database.SetSession(item.GroupID, newSessionID); err != nil {
			log.Printf("processGroup: SetSession %s: %v", item.GroupID, err)
		}
	}

	if out.Status == types.ContainerStatusError {
		return fmt.Errorf("agent error for group %s: %s", item.GroupID, out.Error)
	}

	// Deliver the captured result to the chat (not for scheduled task runs,
	// which typically deliver output via the task's own send_message tool).
	if out.Result != nil && *out.Result != "" && !item.IsTask {
		if err := sender.SendMessage(jid, *out.Result); err != nil {
			log.Printf("processGroup: SendMessage %s: %v", jid, err)
		}
	}

	return nil
}

// buildScript returns the shell script executed for each agent run.
//
// The script passes a ContainerInput JSON payload to the agent container via
// stdin. The entrypoint uses --resume <sessionID> when a session ID is
// provided, or starts a new session on first run. It outputs the response and
// the session ID between GOPHERCLAW sentinel markers; processGroup extracts
// the session ID and persists it for the next run.
//
// Volume mounts:
//   - groups/<group>/         → /workspace/group   (rw, agent working dir)
//   - groups/global/          → /workspace/global  (ro, shared context)
//   - groups/<group>/.opencode/ → /home/opencode/.opencode (rw, session state)
//
// The container image is selected by GOPHERCLAW_CONTAINER_IMAGE (default:
// gopherclaw-agent:latest). Build it with ./container/build.sh.
//
// The prompt is read from groups/<group>/pending_message.txt. Channel adapters
// are responsible for writing that file before enqueuing the group item.
func buildScript(groupFolder, sessionID string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
IMAGE="${GOPHERCLAW_CONTAINER_IMAGE:-%s}"
RUNTIME="${CONTAINER_RUNTIME:-docker}"

# Channel adapters write the inbound message here before enqueuing.
PROMPT=$(cat "groups/%s/pending_message.txt" 2>/dev/null || true)

# Encode input as JSON; jq handles special characters in PROMPT safely.
INPUT=$(jq -cn --arg p "$PROMPT" --arg s "%s" --arg g "%s" \
  '{Prompt:$p,SessionID:$s,GroupFolder:$g}')

# Ensure per-group session state directory exists on the host.
mkdir -p "groups/%s/.opencode"

# The container entrypoint outputs SESSION_ID:<id> then the response,
# all between GOPHERCLAW sentinel markers.
"$RUNTIME" run --rm -i \
  -v "$(pwd)/groups/%s:/workspace/group:rw" \
  -v "$(pwd)/groups/global:/workspace/global:ro" \
  -v "$(pwd)/groups/%s/.opencode:/home/opencode/.opencode:rw" \
  "$IMAGE" <<< "$INPUT"
`,
		"gopherclaw-agent:latest", groupFolder, sessionID, groupFolder, groupFolder, groupFolder, groupFolder)
}

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

	// Create the group queue.
	q := queue.New(cfg.MaxConcurrent, 2*time.Second, 0)
	if cfg.CloseDir != "" {
		q.SetCloseDir(cfg.CloseDir)
	}

	// ch is declared before the closures below so they capture it by reference.
	// The for loop below may update it to a real adapter before Connect is called.
	var ch types.Channel = noopChannel{}

	onMessage := func(chatJID string, msg types.NewMessage) {
		if allowlist.ShouldDropMessage(al, chatJID, msg.Sender) {
			return
		}
		if err := database.StoreMessage(msg); err != nil {
			log.Printf("gopherclaw: StoreMessage: %v", err)
		}
		if err := database.StoreChatMetadata(chatJID, chatJID, true, msg.Timestamp); err != nil {
			log.Printf("gopherclaw: StoreChatMetadata: %v", err)
		}
		group, err := database.GetRegisteredGroup(chatJID)
		if err != nil || group == nil {
			return // room not registered; ignore
		}
		if !allowlist.IsTriggerAllowed(al, chatJID, msg.Sender) {
			return
		}
		item := queue.Item{GroupID: group.Folder, ChatJID: chatJID}
		q.Enqueue(item, func(it queue.Item) error {
			return processGroup(it, database, ch, cfg.AgentTimeout)
		})
	}

	onMetadata := func(chatJID, name string, isGroup bool, _ types.Sender) {
		if err := database.StoreChatMetadata(chatJID, name, isGroup, 0); err != nil {
			log.Printf("gopherclaw: StoreChatMetadata (meta): %v", err)
		}
	}

	// Instantiate all registered adapters (self-registered via init() in skill packages).
	registered := channels.All(onMessage, onMetadata)
	if len(registered) == 0 {
		log.Println("gopherclaw: no channel adapter registered; running in no-op mode. " +
			"Apply a skill branch to add a channel, e.g. `git merge skill/matrix`.")
	}
	for _, c := range registered {
		ch = c
		break // single active channel; multi-channel routing is a future extension
	}

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

	log.Printf("gopherclaw: started (db=%s, maxConcurrent=%d, schedulerPoll=%s, containerImage=%s)",
		cfg.DBPath, cfg.MaxConcurrent, cfg.SchedulerPoll, cfg.ContainerImage)

	// Block until shutdown signal.
	<-ctx.Done()
	log.Println("gopherclaw: shutdown signal received, draining queue…")
	// Shutdown prevents new items from being accepted and waits for the
	// currently-queued items to drain. Containers that are already running
	// are not interrupted; their output will be delivered before exit.
	// Note: in-flight runs that outlast the OS-level grace period will be
	// terminated by the process manager (e.g. systemd's TimeoutStopSec).
	q.Shutdown()
	log.Println("gopherclaw: exited cleanly")
}
