// Package channels provides the channel adapter registry.
// Each adapter's init() function calls Register when its credentials are present.
package channels

import (
	"sync"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// Factory creates a Channel given the two inbound callbacks.
type Factory func(onMsg types.OnInboundMessage, onMeta types.OnChatMetadata) types.Channel

var (
	mu       sync.Mutex
	registry = map[string]Factory{}
)

// Register adds a named channel factory. Called from adapter init() functions.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = f
}

// All instantiates every registered channel keyed by name.
func All(onMsg types.OnInboundMessage, onMeta types.OnChatMetadata) map[string]types.Channel {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]types.Channel, len(registry))
	for name, f := range registry {
		out[name] = f(onMsg, onMeta)
	}
	return out
}
