package allowlist

import (
	"encoding/json"
	"log"
	"os"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// defaultConfig is returned whenever the allowlist file is missing or invalid.
func defaultConfig() *types.AllowlistConfig {
	return &types.AllowlistConfig{
		Allow:     "*",
		Mode:      types.AllowModeTrigger,
		LogDenied: true,
	}
}

// rawConfig is used for JSON unmarshalling before validation.
type rawConfig struct {
	Allow     json.RawMessage            `json:"allow"`
	Mode      string                     `json:"mode"`
	LogDenied *bool                      `json:"log_denied"`
	PerChat   map[string]rawChatConfig   `json:"per_chat"`
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

// parseAllow interprets a raw JSON value as either "*" or []string.
// Returns (value, true) on success, ("*", false) if the value is invalid.
func parseAllow(raw json.RawMessage) (interface{}, bool) {
	if len(raw) == 0 {
		// missing "allow" field — treat as invalid
		return nil, false
	}

	// Try string first
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "*" {
			return "*", true
		}
		return nil, false
	}

	// Try array
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	senders := make([]string, 0, len(arr))
	for _, item := range arr {
		var sender string
		if err := json.Unmarshal(item, &sender); err != nil {
			// Non-string item — invalid
			return nil, false
		}
		senders = append(senders, sender)
	}
	return senders, true
}

func parseMode(s string) types.AllowMode {
	switch types.AllowMode(s) {
	case types.AllowModeDrop:
		return types.AllowModeDrop
	default:
		return types.AllowModeTrigger
	}
}

// effectiveConfig returns the allow/mode applicable to a specific chat,
// falling back to the global config.
func effectiveConfig(cfg *types.AllowlistConfig, chatJID string) (interface{}, types.AllowMode) {
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

// IsSenderAllowed returns true if the sender is permitted in this chat.
func IsSenderAllowed(cfg *types.AllowlistConfig, chatJID, sender string) bool {
	allow, _ := effectiveConfig(cfg, chatJID)
	return checkAllow(allow, sender)
}

// ShouldDropMessage returns true when drop mode is active and the sender is not allowed.
func ShouldDropMessage(cfg *types.AllowlistConfig, chatJID, sender string) bool {
	allow, mode := effectiveConfig(cfg, chatJID)
	if mode != types.AllowModeDrop {
		return false
	}
	return !checkAllow(allow, sender)
}

// IsTriggerAllowed returns true if the sender passes the allowlist check.
// When logDenied is true and the sender is blocked, a log line is emitted.
func IsTriggerAllowed(cfg *types.AllowlistConfig, chatJID, sender string, logDenied bool) bool {
	allowed := IsSenderAllowed(cfg, chatJID, sender)
	if !allowed && (logDenied || cfg.LogDenied) {
		log.Printf("allowlist: denied sender %q in chat %q", sender, chatJID)
	}
	return allowed
}

func checkAllow(allow interface{}, sender string) bool {
	switch v := allow.(type) {
	case string:
		return v == "*"
	case []string:
		for _, s := range v {
			if s == sender {
				return true
			}
		}
		return false
	default:
		return false
	}
}
