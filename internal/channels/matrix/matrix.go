// Package matrix implements a Matrix homeserver channel adapter for gopherclaw.
// It uses mautrix-go for the Matrix client protocol.
package matrix

import (
	"context"
	"fmt"
	"log"
	"sync"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// matrixClientAPI is the subset of mautrix.Client used by Adapter.
// Extracted as an interface so tests can inject a mock without a real server.
type matrixClientAPI interface {
	SendText(ctx context.Context, roomID id.RoomID, text string) (*mautrix.RespSendEvent, error)
	SyncWithContext(ctx context.Context) error
}

// Adapter implements types.Channel for a Matrix homeserver.
// Inbound messages are delivered via the OnInboundMessage callback passed to New.
type Adapter struct {
	homeserver  string
	userID      id.UserID
	accessToken string

	client matrixClientAPI // nil until Connect is called
	cancel context.CancelFunc
	wg     sync.WaitGroup

	onMessage  types.OnInboundMessage
	onMetadata types.OnChatMetadata
}

// New constructs an Adapter. Call Connect to establish the session.
func New(homeserver, userID, accessToken string,
	onMsg types.OnInboundMessage, onMeta types.OnChatMetadata) *Adapter {
	return &Adapter{
		homeserver:  homeserver,
		userID:      id.UserID(userID),
		accessToken: accessToken,
		onMessage:   onMsg,
		onMetadata:  onMeta,
	}
}

// Connect authenticates to the homeserver and starts the sync loop.
func (a *Adapter) Connect() error {
	cli, err := mautrix.NewClient(a.homeserver, a.userID, a.accessToken)
	if err != nil {
		return fmt.Errorf("matrix: NewClient: %w", err)
	}

	syncer := mautrix.NewDefaultSyncer()
	syncer.OnEventType(event.EventMessage, a.handleMessage)
	cli.Syncer = syncer
	a.client = cli

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := a.client.SyncWithContext(ctx); err != nil && ctx.Err() == nil {
			log.Printf("matrix: sync: %v", err)
		}
	}()
	return nil
}

// Disconnect stops the sync loop and waits for it to exit.
func (a *Adapter) Disconnect() error {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	return nil
}

// SendMessage delivers text to a Matrix room identified by chatJID (the room ID).
func (a *Adapter) SendMessage(chatJID, text string) error {
	if a.client == nil {
		return fmt.Errorf("matrix: not connected")
	}
	_, err := a.client.SendText(context.Background(), id.RoomID(chatJID), text)
	if err != nil {
		return fmt.Errorf("matrix: SendText %s: %w", chatJID, err)
	}
	return nil
}

// handleMessage is registered as the EventMessage handler on the syncer.
func (a *Adapter) handleMessage(_ context.Context, evt *event.Event) {
	if evt.Sender == a.userID {
		return // ignore own messages
	}
	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok || content.MsgType != event.MsgText {
		return // ignore non-text events (images, files, etc.)
	}

	chatJID := string(evt.RoomID)
	msg := types.NewMessage{
		ID:        string(evt.ID),
		ChatJID:   chatJID,
		Sender:    string(evt.Sender),
		Content:   content.Body,
		Timestamp: evt.Timestamp / 1000, // Matrix uses milliseconds; gopherclaw uses seconds
		IsFromMe:  false,
	}
	a.onMessage(chatJID, msg)
	// The room display name requires a separate m.room.name state event lookup.
	// We pass the room ID as a placeholder name; it will be overwritten if the
	// host stores a better name via a future state event handler.
	a.onMetadata(chatJID, chatJID, true, a)
}
