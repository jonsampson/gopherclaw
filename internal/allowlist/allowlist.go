// Package allowlist implements sender-based access control for incoming messages.
// Configuration is loaded from a JSON file; malformed or missing files fall back
// to a permissive wildcard default so that misconfiguration never silently drops
// all messages.
package allowlist

import (
	"encoding/json"
	"log"
	"os"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// defaultConfig is returned whenever the allowlist file is missing or invalid.
// It permits all senders in trigger mode so no messages are silently dropped.
func defaultConfig() *types.AllowlistConfig {
	return &types.AllowlistConfig{
		Allow:     types.AllowEveryone(),
		Mode:      types.AllowModeTrigger,
		LogDenied: true,
	}
}

// rawConfig is the intermediate JSON shape before validation and type-mapping.
type rawConfig struct {
	Allow     json.RawMessage          `json:"allow"`
	Mode      string                   `json:"mode"`
	LogDenied *bool                    `json:"log_denied"`
	PerChat   map[string]rawChatConfig `json:"per_chat"`
}

type rawChatConfig struct {
	Allow     json.RawMessage `json:"allow"`
	Mode      string          `json:"mode"`
	LogDenied *bool           `json:"log_denied"`
}

// LoadAllowlist reads and validates the allowlist JSON at path.
// Returns a permissive default if the file is absent or malformed.
func LoadAllowlist(path string) *types.AllowlistConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultConfig()
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return defaultConfig()
	}

	allow, ok := parseAllow(raw.Allow)
	if !ok {
		return defaultConfig()
	}

	mode := parseMode(raw.Mode)
	logDenied := true
	if raw.LogDenied != nil {
		logDenied = *raw.LogDenied
	}

	cfg := &types.AllowlistConfig{
		Allow:     allow,
		Mode:      mode,
		LogDenied: logDenied,
	}

	if len(raw.PerChat) > 0 {
		cfg.PerChat = make(map[string]types.ChatAllowlistConfig, len(raw.PerChat))
		for jid, rc := range raw.PerChat {
			pcAllow, ok := parseAllow(rc.Allow)
			if !ok {
				// Invalid per-chat entry: skip rather than rejecting the whole file.
				continue
			}
			cfg.PerChat[jid] = types.ChatAllowlistConfig{
				Allow:     pcAllow,
				Mode:      parseMode(rc.Mode),
				LogDenied: rc.LogDenied,
			}
		}
	}

	return cfg
}

// parseAllow converts a raw JSON value into a typed AllowRule.
// Accepts either the string "*" (everyone) or a JSON array of strings (allowlist).
// Returns (zero AllowRule, false) for any other value, including non-string array items.
func parseAllow(raw json.RawMessage) (types.AllowRule, bool) {
	if len(raw) == 0 {
		return types.AllowRule{}, false
	}

	// Try the wildcard string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "*" {
			return types.AllowEveryone(), true
		}
		return types.AllowRule{}, false
	}

	// Try a JSON array.
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return types.AllowRule{}, false
	}
	senders := make([]string, 0, len(arr))
	for _, item := range arr {
		var sender string
		if err := json.Unmarshal(item, &sender); err != nil {
			// Reject arrays that contain any non-string element.
			return types.AllowRule{}, false
		}
		senders = append(senders, sender)
	}
	return types.AllowOnly(senders), true
}

func parseMode(s string) types.AllowMode {
	if types.AllowMode(s) == types.AllowModeDrop {
		return types.AllowModeDrop
	}
	return types.AllowModeTrigger
}

// effectiveConfig resolves the allow rule and mode for a specific chat,
// falling back to the global config when no per-chat override exists.
func effectiveConfig(cfg *types.AllowlistConfig, chatJID string) (types.AllowRule, types.AllowMode) {
	if cfg.PerChat != nil {
		if pc, ok := cfg.PerChat[chatJID]; ok {
			mode := pc.Mode
			if mode == "" {
				mode = cfg.Mode
			}
			return pc.Allow, mode
		}
	}
	return cfg.Allow, cfg.Mode
}

// IsSenderAllowed returns true if the sender is permitted to interact in chatJID.
func IsSenderAllowed(cfg *types.AllowlistConfig, chatJID, sender string) bool {
	allow, _ := effectiveConfig(cfg, chatJID)
	return allow.Allows(sender)
}

// ShouldDropMessage returns true when drop mode is active and the sender is not
// on the allowlist. Callers should discard the message without further processing.
func ShouldDropMessage(cfg *types.AllowlistConfig, chatJID, sender string) bool {
	allow, mode := effectiveConfig(cfg, chatJID)
	if mode != types.AllowModeDrop {
		return false
	}
	return !allow.Allows(sender)
}

// IsTriggerAllowed returns true if the sender passes the allowlist check for chatJID.
// When cfg.LogDenied is true and the sender is blocked, the denial is logged.
func IsTriggerAllowed(cfg *types.AllowlistConfig, chatJID, sender string) bool {
	allowed := IsSenderAllowed(cfg, chatJID, sender)
	if !allowed && cfg.LogDenied {
		log.Printf("allowlist: denied sender %q in chat %q", sender, chatJID)
	}
	return allowed
}
