package allowlist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonsampson/gopherclaw/internal/allowlist"
	"github.com/jonsampson/gopherclaw/internal/types"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	return path
}

// ---- LoadAllowlist ----

func TestLoadAllowlist_MissingFile_Defaults(t *testing.T) {
	cfg := allowlist.LoadAllowlist("/nonexistent/path/allowlist.json")
	if cfg.Allow != "*" {
		t.Errorf("expected allow='*', got %v", cfg.Allow)
	}
	if cfg.Mode != types.AllowModeTrigger {
		t.Errorf("expected mode=trigger, got %v", cfg.Mode)
	}
	if !cfg.LogDenied {
		t.Error("expected log_denied=true by default")
	}
}

func TestLoadAllowlist_ValidWildcard(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{"allow":"*","mode":"drop","log_denied":false}`)
	cfg := allowlist.LoadAllowlist(path)
	if cfg.Allow != "*" {
		t.Errorf("expected '*', got %v", cfg.Allow)
	}
	if cfg.Mode != types.AllowModeDrop {
		t.Errorf("expected drop, got %v", cfg.Mode)
	}
	if cfg.LogDenied {
		t.Error("expected log_denied=false")
	}
}

func TestLoadAllowlist_DenyAll(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{"allow":[],"mode":"trigger","log_denied":true}`)
	cfg := allowlist.LoadAllowlist(path)
	senders, ok := cfg.Allow.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", cfg.Allow)
	}
	if len(senders) != 0 {
		t.Errorf("expected empty list, got %v", senders)
	}
}

func TestLoadAllowlist_SenderArray(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{"allow":["alice","bob"],"mode":"drop","log_denied":false}`)
	cfg := allowlist.LoadAllowlist(path)
	senders, ok := cfg.Allow.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", cfg.Allow)
	}
	if len(senders) != 2 || senders[0] != "alice" || senders[1] != "bob" {
		t.Errorf("unexpected senders: %v", senders)
	}
}

func TestLoadAllowlist_PerChatOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{
		"allow":"*","mode":"trigger","log_denied":true,
		"per_chat":{"chat1@g.us":{"allow":["carol"],"mode":"drop"}}
	}`)
	cfg := allowlist.LoadAllowlist(path)
	pc, ok := cfg.PerChat["chat1@g.us"]
	if !ok {
		t.Fatal("per_chat override missing")
	}
	senders, ok := pc.Allow.([]string)
	if !ok || len(senders) != 1 || senders[0] != "carol" {
		t.Errorf("unexpected per_chat allow: %v", pc.Allow)
	}
	if pc.Mode != types.AllowModeDrop {
		t.Errorf("expected drop mode in per_chat, got %v", pc.Mode)
	}
}

func TestLoadAllowlist_InvalidJSON_FallbackDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `not json at all`)
	cfg := allowlist.LoadAllowlist(path)
	if cfg.Allow != "*" {
		t.Errorf("expected '*' fallback, got %v", cfg.Allow)
	}
}

func TestLoadAllowlist_InvalidSchema_FallbackDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{"foo":"bar"}`)
	cfg := allowlist.LoadAllowlist(path)
	if cfg.Allow != "*" {
		t.Errorf("expected '*' fallback on invalid schema, got %v", cfg.Allow)
	}
}

func TestLoadAllowlist_NonStringArrayItems_FallbackDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{"allow":[1,null,true],"mode":"trigger"}`)
	cfg := allowlist.LoadAllowlist(path)
	if cfg.Allow != "*" {
		t.Errorf("expected '*' fallback on non-string items, got %v", cfg.Allow)
	}
}

// ---- IsSenderAllowed ----

func TestIsSenderAllowed_Wildcard(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: "*", Mode: types.AllowModeTrigger}
	if !allowlist.IsSenderAllowed(cfg, "chat@g.us", "anyone") {
		t.Error("wildcard should allow anyone")
	}
}

func TestIsSenderAllowed_EmptyListBlocksAll(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{}, Mode: types.AllowModeTrigger}
	if allowlist.IsSenderAllowed(cfg, "chat@g.us", "alice") {
		t.Error("empty list should block all senders")
	}
}

func TestIsSenderAllowed_ExactMatch(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{"alice"}, Mode: types.AllowModeTrigger}
	if !allowlist.IsSenderAllowed(cfg, "chat@g.us", "alice") {
		t.Error("alice should be allowed")
	}
	if allowlist.IsSenderAllowed(cfg, "chat@g.us", "bob") {
		t.Error("bob should not be allowed")
	}
}

func TestIsSenderAllowed_PerChatOverride(t *testing.T) {
	cfg := &types.AllowlistConfig{
		Allow: "*",
		Mode:  types.AllowModeTrigger,
		PerChat: map[string]types.ChatAllowlistConfig{
			"restricted@g.us": {Allow: []string{"carol"}, Mode: types.AllowModeTrigger},
		},
	}
	// wildcard applies to other chats
	if !allowlist.IsSenderAllowed(cfg, "other@g.us", "anyone") {
		t.Error("wildcard should apply to non-overridden chat")
	}
	// per-chat rule applies
	if !allowlist.IsSenderAllowed(cfg, "restricted@g.us", "carol") {
		t.Error("carol should be allowed in restricted chat")
	}
	if allowlist.IsSenderAllowed(cfg, "restricted@g.us", "dave") {
		t.Error("dave should not be allowed in restricted chat")
	}
}

// ---- ShouldDropMessage ----

func TestShouldDropMessage_TriggerMode(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{"alice"}, Mode: types.AllowModeTrigger}
	if allowlist.ShouldDropMessage(cfg, "chat@g.us", "bob") {
		t.Error("trigger mode should not drop messages")
	}
}

func TestShouldDropMessage_DropMode(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{"alice"}, Mode: types.AllowModeDrop}
	if !allowlist.ShouldDropMessage(cfg, "chat@g.us", "bob") {
		t.Error("drop mode should drop disallowed sender")
	}
}

func TestShouldDropMessage_AllowedSenderNotDropped(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{"alice"}, Mode: types.AllowModeDrop}
	if allowlist.ShouldDropMessage(cfg, "chat@g.us", "alice") {
		t.Error("allowed sender should not be dropped even in drop mode")
	}
}

func TestShouldDropMessage_PerChatMode(t *testing.T) {
	cfg := &types.AllowlistConfig{
		Allow: "*",
		Mode:  types.AllowModeTrigger,
		PerChat: map[string]types.ChatAllowlistConfig{
			"strict@g.us": {Allow: []string{"alice"}, Mode: types.AllowModeDrop},
		},
	}
	// global: trigger — don't drop
	if allowlist.ShouldDropMessage(cfg, "other@g.us", "anyone") {
		t.Error("global trigger mode should not drop")
	}
	// per-chat: drop mode
	if !allowlist.ShouldDropMessage(cfg, "strict@g.us", "bob") {
		t.Error("per-chat drop mode should drop non-allowed sender")
	}
}

// ---- IsTriggerAllowed ----

func TestIsTriggerAllowed_AllowedSender(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{"alice"}, Mode: types.AllowModeTrigger}
	if !allowlist.IsTriggerAllowed(cfg, "chat@g.us", "alice", false) {
		t.Error("alice should pass trigger check")
	}
}

func TestIsTriggerAllowed_DisallowedSender(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{"alice"}, Mode: types.AllowModeTrigger}
	if allowlist.IsTriggerAllowed(cfg, "chat@g.us", "bob", false) {
		t.Error("bob should fail trigger check")
	}
}

func TestIsTriggerAllowed_LoggingDoesNotPanic(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: []string{"alice"}, Mode: types.AllowModeTrigger, LogDenied: true}
	// should not panic
	allowlist.IsTriggerAllowed(cfg, "chat@g.us", "eve", true)
}
