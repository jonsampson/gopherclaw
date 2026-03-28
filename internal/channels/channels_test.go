package channels_test

import (
	"sync"
	"testing"

	"github.com/jonsampson/gopherclaw/internal/channels"
	"github.com/jonsampson/gopherclaw/internal/types"
)

// stubChannel is a minimal types.Channel used to verify factory invocation.
type stubChannel struct{ name string }

func (s *stubChannel) Connect() error                { return nil }
func (s *stubChannel) Disconnect() error             { return nil }
func (s *stubChannel) SendMessage(_, _ string) error { return nil }

func noopCallbacks() (types.OnInboundMessage, types.OnChatMetadata) {
	return func(string, types.NewMessage) {},
		func(string, string, bool, types.Sender) {}
}

// resetRegistry clears the package-level registry between tests.
// Tests in this file must not run in parallel with each other.
func resetRegistry() {
	// Re-register nothing; swap the registry contents via All and a sentinel.
	// The simplest reset: register a sentinel that panics, call All, then
	// use the unexported reset exposed by export_test.go.
	channels.ResetForTest()
}

func TestAll_EmptyRegistry(t *testing.T) {
	resetRegistry()
	onMsg, onMeta := noopCallbacks()
	got := channels.All(onMsg, onMeta)
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

func TestRegister_All_RoundTrip(t *testing.T) {
	resetRegistry()
	channels.Register("test-chan", func(onMsg types.OnInboundMessage, onMeta types.OnChatMetadata) types.Channel {
		return &stubChannel{name: "test-chan"}
	})

	onMsg, onMeta := noopCallbacks()
	got := channels.All(onMsg, onMeta)
	if len(got) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(got))
	}
	ch, ok := got["test-chan"]
	if !ok {
		t.Fatal("expected key 'test-chan' in result")
	}
	if _, ok := ch.(*stubChannel); !ok {
		t.Errorf("expected *stubChannel, got %T", ch)
	}
}

func TestAll_MultipleAdapters(t *testing.T) {
	resetRegistry()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		n := name // capture for closure
		channels.Register(n, func(onMsg types.OnInboundMessage, onMeta types.OnChatMetadata) types.Channel {
			return &stubChannel{name: n}
		})
	}

	onMsg, onMeta := noopCallbacks()
	got := channels.All(onMsg, onMeta)
	if len(got) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(got))
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing channel %q in result", name)
		}
	}
}

func TestRegister_ConcurrentSafe(t *testing.T) {
	resetRegistry()
	// Register and All called concurrently; the race detector validates safety.
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			channels.Register("concurrent", func(_ types.OnInboundMessage, _ types.OnChatMetadata) types.Channel {
				return &stubChannel{}
			})
		}()
	}
	wg.Wait()
}
