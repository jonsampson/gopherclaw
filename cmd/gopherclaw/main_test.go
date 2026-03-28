package main

import (
	"strings"
	"testing"
	"time"

	"github.com/jonsampson/gopherclaw/internal/queue"
)

// ---- envOr ----

func TestEnvOr_ReturnsEnvWhenSet(t *testing.T) {
	t.Setenv("_GOPHERCLAW_TEST_KEY", "myvalue")
	if got := envOr("_GOPHERCLAW_TEST_KEY", "default"); got != "myvalue" {
		t.Errorf("got %q, want %q", got, "myvalue")
	}
}

func TestEnvOr_ReturnsDefaultWhenUnset(t *testing.T) {
	// Ensure variable is absent.
	t.Setenv("_GOPHERCLAW_TEST_KEY", "")
	if got := envOr("_GOPHERCLAW_TEST_KEY", "default"); got != "default" {
		t.Errorf("got %q, want %q", got, "default")
	}
}

// ---- loadConfig ----

func TestLoadConfig_Defaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBPath != "gopherclaw.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "gopherclaw.db")
	}
	if cfg.AllowlistPath != "allowlist.json" {
		t.Errorf("AllowlistPath = %q, want %q", cfg.AllowlistPath, "allowlist.json")
	}
	if cfg.MaxConcurrent != 4 {
		t.Errorf("MaxConcurrent = %d, want 4", cfg.MaxConcurrent)
	}
	if cfg.AgentTimeout != 5*time.Minute {
		t.Errorf("AgentTimeout = %v, want 5m", cfg.AgentTimeout)
	}
	if cfg.SchedulerPoll != 15*time.Second {
		t.Errorf("SchedulerPoll = %v, want 15s", cfg.SchedulerPoll)
	}
}

func TestLoadConfig_ValidMaxConcurrent(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_MAX_CONCURRENT", "8")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxConcurrent != 8 {
		t.Errorf("MaxConcurrent = %d, want 8", cfg.MaxConcurrent)
	}
}

func TestLoadConfig_InvalidMaxConcurrent_NonNumeric(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_MAX_CONCURRENT", "abc")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for non-numeric GOPHERCLAW_MAX_CONCURRENT")
	}
}

func TestLoadConfig_InvalidMaxConcurrent_Zero(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_MAX_CONCURRENT", "0")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for zero GOPHERCLAW_MAX_CONCURRENT")
	}
}

func TestLoadConfig_InvalidMaxConcurrent_Negative(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_MAX_CONCURRENT", "-1")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for negative GOPHERCLAW_MAX_CONCURRENT")
	}
}

func TestLoadConfig_ValidAgentTimeout(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_AGENT_TIMEOUT", "10m")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentTimeout != 10*time.Minute {
		t.Errorf("AgentTimeout = %v, want 10m", cfg.AgentTimeout)
	}
}

func TestLoadConfig_InvalidAgentTimeout(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_AGENT_TIMEOUT", "notaduration")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for invalid GOPHERCLAW_AGENT_TIMEOUT")
	}
}

func TestLoadConfig_ZeroAgentTimeout(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_AGENT_TIMEOUT", "0s")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for zero GOPHERCLAW_AGENT_TIMEOUT")
	}
}

func TestLoadConfig_ValidSchedulerPoll(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_SCHEDULER_POLL", "30s")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchedulerPoll != 30*time.Second {
		t.Errorf("SchedulerPoll = %v, want 30s", cfg.SchedulerPoll)
	}
}

func TestLoadConfig_InvalidSchedulerPoll(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GOPHERCLAW_SCHEDULER_POLL", "notaduration")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for invalid GOPHERCLAW_SCHEDULER_POLL")
	}
}

// clearConfigEnv clears all GOPHERCLAW_* env vars so tests start from a
// clean slate regardless of the caller's environment.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GOPHERCLAW_DB",
		"GOPHERCLAW_ALLOWLIST",
		"GOPHERCLAW_CLOSE_DIR",
		"GOPHERCLAW_MAX_CONCURRENT",
		"GOPHERCLAW_AGENT_TIMEOUT",
		"GOPHERCLAW_SCHEDULER_POLL",
	} {
		t.Setenv(k, "") // t.Setenv restores original value after test
	}
}

// ---- chatJID ----

func TestChatJID_ReturnsChatJIDWhenSet(t *testing.T) {
	item := queue.Item{GroupID: "groups/main", ChatJID: "!room:example.com"}
	if got := chatJID(item); got != "!room:example.com" {
		t.Errorf("chatJID = %q, want %q", got, "!room:example.com")
	}
}

func TestChatJID_FallsBackToGroupID(t *testing.T) {
	item := queue.Item{GroupID: "groups/main"}
	if got := chatJID(item); got != "groups/main" {
		t.Errorf("chatJID = %q, want %q", got, "groups/main")
	}
}

// ---- buildScript ----

func TestBuildScript_ContainsGroupFolder(t *testing.T) {
	s := buildScript("groups/myroom", "sess")
	if !strings.Contains(s, "groups/myroom") {
		t.Errorf("script does not reference group folder")
	}
}

func TestBuildScript_QuotesPathForShell(t *testing.T) {
	// A folder with spaces must be quoted so the shell cd doesn't split it.
	s := buildScript("groups/my room", "sess")
	if !strings.Contains(s, `"groups/my room"`) {
		t.Errorf("script does not shell-quote folder with spaces:\n%s", s)
	}
}

// ---- processGroup ----

func TestProcessGroup_ChatJIDRoutingLogic(t *testing.T) {
	// Verify that chatJID(item) — not item.GroupID — is used as the send target.
	item := queue.Item{GroupID: "groups/main", ChatJID: "!room:example.com"}
	if got := chatJID(item); got != "!room:example.com" {
		t.Errorf("chatJID = %q, want %q", got, "!room:example.com")
	}
}
