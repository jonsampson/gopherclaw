// Package allowlist_test exercises the sender allowlist loading and evaluation logic.
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
	if !cfg.Allow.IsWildcard() {
		t.Errorf("expected wildcard allow by default, got list: %v", cfg.Allow.List())
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
	if !cfg.Allow.IsWildcard() {
		t.Errorf("expected wildcard, got list: %v", cfg.Allow.List())
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
	if cfg.Allow.IsWildcard() {
		t.Fatal("expected explicit list, got wildcard")
	}
	if len(cfg.Allow.List()) != 0 {
		t.Errorf("expected empty list, got %v", cfg.Allow.List())
	}
}

func TestLoadAllowlist_SenderArray(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{"allow":["alice","bob"],"mode":"drop","log_denied":false}`)
	cfg := allowlist.LoadAllowlist(path)
	if cfg.Allow.IsWildcard() {
		t.Fatal("expected list, got wildcard")
	}
	senders := cfg.Allow.List()
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
	if pc.Allow.IsWildcard() {
		t.Fatal("per_chat: expected list, got wildcard")
	}
	senders := pc.Allow.List()
	if len(senders) != 1 || senders[0] != "carol" {
		t.Errorf("unexpected per_chat senders: %v", senders)
	}
	if pc.Mode != types.AllowModeDrop {
		t.Errorf("expected drop mode in per_chat, got %v", pc.Mode)
	}
}

func TestLoadAllowlist_InvalidJSON_FallbackDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `not json at all`)
	cfg := allowlist.LoadAllowlist(path)
	if !cfg.Allow.IsWildcard() {
		t.Errorf("expected wildcard fallback on invalid JSON, got list: %v", cfg.Allow.List())
	}
}

func TestLoadAllowlist_InvalidSchema_FallbackDefault(t *testing.T) {
	dir := t.TempDir()
	// Valid JSON but missing required "allow" field → fallback.
	path := writeFile(t, dir, "a.json", `{"foo":"bar"}`)
	cfg := allowlist.LoadAllowlist(path)
	if !cfg.Allow.IsWildcard() {
		t.Errorf("expected wildcard fallback on invalid schema, got list: %v", cfg.Allow.List())
	}
}

func TestLoadAllowlist_NonStringArrayItems_FallbackDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.json", `{"allow":[1,null,true],"mode":"trigger"}`)
	cfg := allowlist.LoadAllowlist(path)
	if !cfg.Allow.IsWildcard() {
		t.Errorf("expected wildcard fallback on non-string items, got list: %v", cfg.Allow.List())
	}
}

func TestLoadAllowlist_InvalidPerChatEntry_SkippedNotRejected(t *testing.T) {
	dir := t.TempDir()
	// One valid and one invalid per-chat entry: the whole file should still load.
	path := writeFile(t, dir, "a.json", `{
		"allow":"*","mode":"trigger","log_denied":false,
		"per_chat":{
			"good@g.us":{"allow":["alice"],"mode":"trigger"},
			"bad@g.us":{"allow":42}
		}
	}`)
	cfg := allowlist.LoadAllowlist(path)
	if !cfg.Allow.IsWildcard() {
		t.Error("global rule should still be wildcard")
	}
	if _, ok := cfg.PerChat["good@g.us"]; !ok {
		t.Error("valid per-chat entry should be present")
	}
	if _, ok := cfg.PerChat["bad@g.us"]; ok {
		t.Error("invalid per-chat entry should be skipped")
	}
}

// ---- IsSenderAllowed ----

func TestIsSenderAllowed_Wildcard(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowEveryone(), Mode: types.AllowModeTrigger}
	if !allowlist.IsSenderAllowed(cfg, "chat@g.us", "anyone") {
		t.Error("wildcard should allow any sender")
	}
}

func TestIsSenderAllowed_EmptyListBlocksAll(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowOnly(nil), Mode: types.AllowModeTrigger}
	if allowlist.IsSenderAllowed(cfg, "chat@g.us", "alice") {
		t.Error("empty allowlist should block all senders")
	}
}

func TestIsSenderAllowed_ExactMatch(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowOnly([]string{"alice"}), Mode: types.AllowModeTrigger}
	if !allowlist.IsSenderAllowed(cfg, "chat@g.us", "alice") {
		t.Error("alice should be allowed")
	}
	if allowlist.IsSenderAllowed(cfg, "chat@g.us", "bob") {
		t.Error("bob should not be allowed")
	}
}

func TestIsSenderAllowed_PerChatOverride(t *testing.T) {
	cfg := &types.AllowlistConfig{
		Allow: types.AllowEveryone(),
		Mode:  types.AllowModeTrigger,
		PerChat: map[string]types.ChatAllowlistConfig{
			"restricted@g.us": {
				Allow: types.AllowOnly([]string{"carol"}),
				Mode:  types.AllowModeTrigger,
			},
		},
	}
	// Global wildcard applies to non-overridden chats.
	if !allowlist.IsSenderAllowed(cfg, "other@g.us", "anyone") {
		t.Error("wildcard should apply to non-overridden chat")
	}
	// Per-chat rule restricts access.
	if !allowlist.IsSenderAllowed(cfg, "restricted@g.us", "carol") {
		t.Error("carol should be allowed in restricted chat")
	}
	if allowlist.IsSenderAllowed(cfg, "restricted@g.us", "dave") {
		t.Error("dave should not be allowed in restricted chat")
	}
}

// ---- ShouldDropMessage ----

func TestShouldDropMessage_TriggerMode_NeverDrops(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowOnly([]string{"alice"}), Mode: types.AllowModeTrigger}
	if allowlist.ShouldDropMessage(cfg, "chat@g.us", "bob") {
		t.Error("trigger mode should never drop messages")
	}
}

func TestShouldDropMessage_DropMode_DropsDisallowed(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowOnly([]string{"alice"}), Mode: types.AllowModeDrop}
	if !allowlist.ShouldDropMessage(cfg, "chat@g.us", "bob") {
		t.Error("drop mode should drop non-allowed sender")
	}
}

func TestShouldDropMessage_DropMode_KeepsAllowed(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowOnly([]string{"alice"}), Mode: types.AllowModeDrop}
	if allowlist.ShouldDropMessage(cfg, "chat@g.us", "alice") {
		t.Error("drop mode should keep allowed sender")
	}
}

func TestShouldDropMessage_PerChatModeOverride(t *testing.T) {
	cfg := &types.AllowlistConfig{
		Allow: types.AllowEveryone(),
		Mode:  types.AllowModeTrigger,
		PerChat: map[string]types.ChatAllowlistConfig{
			"strict@g.us": {Allow: types.AllowOnly([]string{"alice"}), Mode: types.AllowModeDrop},
		},
	}
	// Global trigger mode: no drops.
	if allowlist.ShouldDropMessage(cfg, "other@g.us", "anyone") {
		t.Error("global trigger mode should not drop")
	}
	// Per-chat drop mode: non-allowed sender dropped.
	if !allowlist.ShouldDropMessage(cfg, "strict@g.us", "bob") {
		t.Error("per-chat drop mode should drop non-allowed sender")
	}
}

// ---- IsTriggerAllowed ----

func TestIsTriggerAllowed_AllowedSender(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowOnly([]string{"alice"}), Mode: types.AllowModeTrigger}
	if !allowlist.IsTriggerAllowed(cfg, "chat@g.us", "alice") {
		t.Error("alice should pass trigger check")
	}
}

func TestIsTriggerAllowed_DisallowedSender(t *testing.T) {
	cfg := &types.AllowlistConfig{Allow: types.AllowOnly([]string{"alice"}), Mode: types.AllowModeTrigger}
	if allowlist.IsTriggerAllowed(cfg, "chat@g.us", "bob") {
		t.Error("bob should fail trigger check")
	}
}

func TestIsTriggerAllowed_LogDenied_DoesNotPanic(t *testing.T) {
	cfg := &types.AllowlistConfig{
		Allow:     types.AllowOnly([]string{"alice"}),
		Mode:      types.AllowModeTrigger,
		LogDenied: true,
	}
	// Should not panic even when logging is enabled for a denied sender.
	allowlist.IsTriggerAllowed(cfg, "chat@g.us", "eve")
}
