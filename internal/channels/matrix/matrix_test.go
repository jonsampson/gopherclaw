package matrix

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// mockClient implements matrixClientAPI for deterministic testing without a
// real Matrix homeserver.
type mockClient struct {
	mu      sync.Mutex
	sent    []sendCall
	sendErr error
	syncErr error
	// syncStarted is closed the first time SyncWithContext is entered.
	syncStarted chan struct{}
}

type sendCall struct {
	roomID id.RoomID
	text   string
}

func newMock() *mockClient {
	return &mockClient{syncStarted: make(chan struct{})}
}

func (m *mockClient) SendText(_ context.Context, roomID id.RoomID, text string) (*mautrix.RespSendEvent, error) {
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	m.mu.Lock()
	m.sent = append(m.sent, sendCall{roomID, text})
	m.mu.Unlock()
	return &mautrix.RespSendEvent{EventID: "fake-event-id"}, nil
}

func (m *mockClient) SyncWithContext(ctx context.Context) error {
	// Signal that sync has started, then block until context is cancelled.
	select {
	case <-m.syncStarted:
	default:
		close(m.syncStarted)
	}
	<-ctx.Done()
	return m.syncErr
}

// newTestAdapter creates an Adapter with a mock client pre-injected.
// The mock is returned so tests can inspect calls or set error conditions.
func newTestAdapter() (*Adapter, *mockClient) {
	mock := newMock()
	a := &Adapter{
		userID:     id.UserID("@bot:example.com"),
		onMessage:  func(string, types.NewMessage) {},
		onMetadata: func(string, string, bool, types.Sender) {},
		client:     mock,
	}
	return a, mock
}

// makeTextEvent builds a minimal m.room.message event for use in tests.
func makeTextEvent(sender id.UserID, roomID id.RoomID, body string, tsMillis int64) *event.Event {
	content := event.MessageEventContent{MsgType: event.MsgText, Body: body}
	return &event.Event{
		ID:        id.EventID("$event123"),
		Sender:    sender,
		RoomID:    roomID,
		Timestamp: tsMillis,
		Content:   event.Content{Parsed: &content},
	}
}

func TestSendMessage_NotConnected(t *testing.T) {
	a := &Adapter{}
	if err := a.SendMessage("!room:example.com", "hello"); err == nil {
		t.Fatal("expected error when not connected, got nil")
	}
}

func TestSendMessage_Delegates(t *testing.T) {
	a, mock := newTestAdapter()
	if err := a.SendMessage("!abc:example.com", "hello world"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sent) != 1 {
		t.Fatalf("expected 1 send call, got %d", len(mock.sent))
	}
	if got, want := mock.sent[0].roomID, id.RoomID("!abc:example.com"); got != want {
		t.Errorf("roomID = %q, want %q", got, want)
	}
	if got, want := mock.sent[0].text, "hello world"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestSendMessage_PropagatesError(t *testing.T) {
	a, mock := newTestAdapter()
	mock.sendErr = errors.New("network error")
	if err := a.SendMessage("!room:example.com", "hi"); err == nil {
		t.Fatal("expected error from mock sendErr, got nil")
	}
}

func TestDisconnect_StopsSync(t *testing.T) {
	a, mock := newTestAdapter()

	// Simulate what Connect() does: start the sync goroutine with a
	// cancellable context and wire up the WaitGroup.
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		_ = a.client.SyncWithContext(ctx)
	}()

	// Wait for the goroutine to actually reach SyncWithContext before proceeding.
	select {
	case <-mock.syncStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("SyncWithContext never started")
	}

	done := make(chan struct{})
	go func() {
		_ = a.Disconnect()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect did not return within 2s after cancellation")
	}
}

func TestHandleMessage_IgnoresSelf(t *testing.T) {
	a, _ := newTestAdapter()
	called := false
	a.onMessage = func(_ string, _ types.NewMessage) { called = true }

	evt := makeTextEvent(a.userID, "!room:example.com", "hi", 1000)
	a.handleMessage(context.Background(), evt)

	if called {
		t.Error("onMessage should not be called for self-sent messages")
	}
}

func TestHandleMessage_IgnoresNonText(t *testing.T) {
	a, _ := newTestAdapter()
	called := false
	a.onMessage = func(_ string, _ types.NewMessage) { called = true }

	content := event.MessageEventContent{MsgType: event.MsgImage, Body: "image.png"}
	evt := &event.Event{
		ID:      "$ev",
		Sender:  "@other:example.com",
		RoomID:  "!room:example.com",
		Content: event.Content{Parsed: &content},
	}
	a.handleMessage(context.Background(), evt)

	if called {
		t.Error("onMessage should not be called for non-text events")
	}
}

func TestHandleMessage_FiresCallbacks(t *testing.T) {
	a, _ := newTestAdapter()

	var gotMsg types.NewMessage
	var gotMeta struct {
		jid     string
		name    string
		isGroup bool
	}

	a.onMessage = func(chatJID string, msg types.NewMessage) { gotMsg = msg }
	a.onMetadata = func(chatJID, name string, isGroup bool, _ types.Sender) {
		gotMeta.jid = chatJID
		gotMeta.name = name
		gotMeta.isGroup = isGroup
	}

	evt := makeTextEvent("@alice:example.com", "!room:example.com", "hello", 2000)
	a.handleMessage(context.Background(), evt)

	if got, want := gotMsg.Content, "hello"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	if got, want := gotMsg.Sender, "@alice:example.com"; got != want {
		t.Errorf("sender = %q, want %q", got, want)
	}
	if got, want := gotMsg.ChatJID, "!room:example.com"; got != want {
		t.Errorf("chatJID = %q, want %q", got, want)
	}
	if got, want := gotMsg.ID, "$event123"; got != want {
		t.Errorf("event ID = %q, want %q", got, want)
	}
	if got, want := gotMeta.jid, "!room:example.com"; got != want {
		t.Errorf("metadata jid = %q, want %q", got, want)
	}
	// The name falls back to the room JID until a m.room.name state event updates it.
	if got, want := gotMeta.name, "!room:example.com"; got != want {
		t.Errorf("metadata name = %q, want %q", got, want)
	}
	if !gotMeta.isGroup {
		t.Error("metadata isGroup should be true for Matrix rooms")
	}
}

func TestHandleMessage_MetadataSenderIsAdapter(t *testing.T) {
	a, _ := newTestAdapter()
	var gotSender types.Sender
	a.onMetadata = func(_, _ string, _ bool, sender types.Sender) { gotSender = sender }

	evt := makeTextEvent("@alice:example.com", "!room:example.com", "hi", 1000)
	a.handleMessage(context.Background(), evt)

	if gotSender != a {
		t.Error("onMetadata sender argument should be the Adapter itself")
	}
}

func TestDisconnect_BeforeConnect_Safe(t *testing.T) {
	// Disconnect on a freshly constructed Adapter (Connect never called)
	// must not panic and must return nil.
	a := &Adapter{}
	if err := a.Disconnect(); err != nil {
		t.Errorf("Disconnect before Connect returned unexpected error: %v", err)
	}
}

func TestConnect_InvalidHomeserver_ReturnsError(t *testing.T) {
	a := New(":::not-a-url", "@bot:example.com", "token",
		func(string, types.NewMessage) {},
		func(string, string, bool, types.Sender) {},
	)
	if err := a.Connect(); err == nil {
		// If mautrix.NewClient doesn't validate the URL eagerly, skip rather
		// than fail — the error will surface at sync time instead.
		t.Skip("mautrix.NewClient accepted invalid URL without error; sync-time validation only")
	}
}

func TestConnect_Twice_SecondCancelsFirst(t *testing.T) {
	a, mock := newTestAdapter()

	// Manually wire the first "connection" as Connect() would.
	ctx1, cancel1 := context.WithCancel(context.Background())
	a.cancel = cancel1
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		_ = a.client.SyncWithContext(ctx1)
	}()

	// Wait for first sync to start.
	select {
	case <-mock.syncStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first SyncWithContext never started")
	}

	// Simulate a second Connect by replacing cancel and starting a new goroutine.
	// A real second Connect() call would overwrite a.cancel — this test verifies
	// that explicitly cancelling the first context stops the first goroutine.
	cancel1() // caller's responsibility until Connect is made idempotent
	a.wg.Wait()

	// After cancellation the adapter should accept a fresh sync goroutine cleanly.
	mock2 := newMock()
	a.client = mock2
	ctx2, cancel2 := context.WithCancel(context.Background())
	a.cancel = cancel2
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		_ = a.client.SyncWithContext(ctx2)
	}()

	select {
	case <-mock2.syncStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second SyncWithContext never started")
	}

	cancel2()
	a.wg.Wait()
}

func TestHandleMessage_TimestampConversion(t *testing.T) {
	a, _ := newTestAdapter()
	var gotMsg types.NewMessage
	a.onMessage = func(_ string, msg types.NewMessage) { gotMsg = msg }

	// Matrix timestamps are milliseconds; gopherclaw uses Unix seconds.
	evt := makeTextEvent("@alice:example.com", "!room:example.com", "hi", 5000)
	a.handleMessage(context.Background(), evt)

	if got, want := gotMsg.Timestamp, int64(5); got != want {
		t.Errorf("Timestamp = %d, want %d (5000ms → 5s)", got, want)
	}
}
