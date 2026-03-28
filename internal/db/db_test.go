package db_test

import (
	"fmt"
	"testing"

	"github.com/jonsampson/gopherclaw/internal/db"
	"github.com/jonsampson/gopherclaw/internal/types"
)

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.InitTestDB()
	if err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// ---- StoreMessage / GetMessagesSince ----

func TestStoreMessage_StoresAndRetrieves(t *testing.T) {
	d := newTestDB(t)
	msg := types.NewMessage{
		ID:        "msg1",
		ChatJID:   "group1@g.us",
		Sender:    "alice",
		Content:   "hello",
		Timestamp: 1000,
		IsFromMe:  false,
	}
	if err := d.StoreMessage(msg); err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}
	msgs, err := d.GetMessagesSince("group1@g.us", 0, "", 100)
	if err != nil {
		t.Fatalf("GetMessagesSince: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	got := msgs[0]
	if got.ID != "msg1" || got.Sender != "alice" || got.Content != "hello" || got.Timestamp != 1000 {
		t.Errorf("unexpected message fields: %+v", got)
	}
}

func TestStoreMessage_FiltersEmptyContent(t *testing.T) {
	d := newTestDB(t)
	msg := types.NewMessage{
		ID:        "msg-empty",
		ChatJID:   "group1@g.us",
		Sender:    "alice",
		Content:   "",
		Timestamp: 1000,
	}
	if err := d.StoreMessage(msg); err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}
	msgs, err := d.GetMessagesSince("group1@g.us", 0, "", 100)
	if err != nil {
		t.Fatalf("GetMessagesSince: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty content to be filtered, got %d messages", len(msgs))
	}
}

func TestStoreMessage_StoresIsFromMe(t *testing.T) {
	d := newTestDB(t)
	msg := types.NewMessage{
		ID:        "msg-bot",
		ChatJID:   "group1@g.us",
		Sender:    "bot",
		Content:   "I am the bot",
		Timestamp: 1000,
		IsFromMe:  true,
	}
	if err := d.StoreMessage(msg); err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}
	// bot messages excluded from GetMessagesSince
	msgs, err := d.GetMessagesSince("group1@g.us", 0, "", 100)
	if err != nil {
		t.Fatalf("GetMessagesSince: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected bot message to be excluded from GetMessagesSince, got %d", len(msgs))
	}
}

func TestStoreMessage_UpsertOnDuplicateIDAndChatJID(t *testing.T) {
	d := newTestDB(t)
	msg := types.NewMessage{
		ID:        "dup1",
		ChatJID:   "group1@g.us",
		Sender:    "alice",
		Content:   "first",
		Timestamp: 1000,
	}
	if err := d.StoreMessage(msg); err != nil {
		t.Fatalf("first StoreMessage: %v", err)
	}
	msg.Content = "updated"
	if err := d.StoreMessage(msg); err != nil {
		t.Fatalf("second StoreMessage: %v", err)
	}
	msgs, err := d.GetMessagesSince("group1@g.us", 0, "", 100)
	if err != nil {
		t.Fatalf("GetMessagesSince: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after upsert, got %d", len(msgs))
	}
	if msgs[0].Content != "updated" {
		t.Errorf("expected content 'updated', got %q", msgs[0].Content)
	}
}

func TestGetMessagesSince_ReturnsAfterTimestamp(t *testing.T) {
	d := newTestDB(t)
	for _, ts := range []int64{100, 200, 300} {
		d.StoreMessage(types.NewMessage{
			ID: fmt.Sprintf("m%d", ts), ChatJID: "g@g.us",
			Sender: "a", Content: "x", Timestamp: ts,
		})
	}
	msgs, err := d.GetMessagesSince("g@g.us", 150, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after ts=150, got %d", len(msgs))
	}
	if msgs[0].Timestamp != 200 || msgs[1].Timestamp != 300 {
		t.Errorf("wrong timestamps: %v", msgs)
	}
}

func TestGetMessagesSince_ExcludesBotMessages(t *testing.T) {
	d := newTestDB(t)
	d.StoreMessage(types.NewMessage{
		ID: "user1", ChatJID: "g@g.us", Sender: "alice", Content: "hi", Timestamp: 100,
	})
	d.StoreMessage(types.NewMessage{
		ID: "bot1", ChatJID: "g@g.us", Sender: "bot", Content: "pong", Timestamp: 200, IsFromMe: true,
	})
	msgs, err := d.GetMessagesSince("g@g.us", 0, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != "user1" {
		t.Errorf("expected only user message, got %v", msgs)
	}
}

func TestGetMessagesSince_AllNonBotWhenNoCursor(t *testing.T) {
	d := newTestDB(t)
	for i := range 5 {
		d.StoreMessage(types.NewMessage{
			ID: fmt.Sprintf("m%d", i), ChatJID: "g@g.us",
			Sender: "a", Content: "x", Timestamp: int64(i + 1),
		})
	}
	msgs, err := d.GetMessagesSince("g@g.us", 0, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Errorf("expected 5 messages with no cursor, got %d", len(msgs))
	}
}

func TestGetMessagesSince_RecoversCursorFromLastBotReply(t *testing.T) {
	d := newTestDB(t)
	// bot message at ts=50
	d.StoreMessage(types.NewMessage{
		ID: "bot1", ChatJID: "g@g.us", Sender: "bot", Content: "reply", Timestamp: 50, IsFromMe: true,
	})
	// user messages before and after
	d.StoreMessage(types.NewMessage{
		ID: "u1", ChatJID: "g@g.us", Sender: "alice", Content: "before", Timestamp: 30,
	})
	d.StoreMessage(types.NewMessage{
		ID: "u2", ChatJID: "g@g.us", Sender: "alice", Content: "after", Timestamp: 70,
	})

	// Pass sinceTimestamp=0 but no lastAgentTimestamp — should recover from bot ts=50
	msgs, err := d.GetMessagesSince("g@g.us", 0, "bot1", 100)
	if err != nil {
		t.Fatal(err)
	}
	// Only the message after the bot reply should be returned
	if len(msgs) != 1 || msgs[0].ID != "u2" {
		t.Errorf("expected only 'after' message, got %v", msgs)
	}
}

func TestGetMessagesSince_CapsToLimit(t *testing.T) {
	d := newTestDB(t)
	for i := range 10 {
		d.StoreMessage(types.NewMessage{
			ID: fmt.Sprintf("m%d", i), ChatJID: "g@g.us",
			Sender: "a", Content: "x", Timestamp: int64(i + 1),
		})
	}
	msgs, err := d.GetMessagesSince("g@g.us", 0, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages (limit), got %d", len(msgs))
	}
	// Should be the 3 most recent in chronological order
	if msgs[0].Timestamp != 8 || msgs[1].Timestamp != 9 || msgs[2].Timestamp != 10 {
		t.Errorf("wrong messages returned: %v", msgs)
	}
}

// ---- GetNewMessages ----

func TestGetNewMessages_AcrossMultipleGroups(t *testing.T) {
	d := newTestDB(t)
	d.StoreMessage(types.NewMessage{ID: "a1", ChatJID: "g1@g.us", Sender: "a", Content: "hi", Timestamp: 100})
	d.StoreMessage(types.NewMessage{ID: "b1", ChatJID: "g2@g.us", Sender: "b", Content: "hey", Timestamp: 200})

	groups := []db.GroupCursor{
		{ChatJID: "g1@g.us", SinceTimestamp: 0, LastBotMsgID: ""},
		{ChatJID: "g2@g.us", SinceTimestamp: 0, LastBotMsgID: ""},
	}
	msgs, err := d.GetNewMessages(groups, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func TestGetNewMessages_FiltersByTimestamp(t *testing.T) {
	d := newTestDB(t)
	d.StoreMessage(types.NewMessage{ID: "a1", ChatJID: "g1@g.us", Sender: "a", Content: "old", Timestamp: 50})
	d.StoreMessage(types.NewMessage{ID: "a2", ChatJID: "g1@g.us", Sender: "a", Content: "new", Timestamp: 150})

	groups := []db.GroupCursor{{ChatJID: "g1@g.us", SinceTimestamp: 100, LastBotMsgID: ""}}
	msgs, err := d.GetNewMessages(groups, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "new" {
		t.Errorf("expected only 'new', got %v", msgs)
	}
}

func TestGetNewMessages_EmptyForNoGroups(t *testing.T) {
	d := newTestDB(t)
	msgs, err := d.GetNewMessages(nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d", len(msgs))
	}
}

func TestGetNewMessages_CapsAndChronologicalOrder(t *testing.T) {
	d := newTestDB(t)
	for i := range 10 {
		d.StoreMessage(types.NewMessage{
			ID: fmt.Sprintf("m%d", i), ChatJID: "g1@g.us",
			Sender: "a", Content: "x", Timestamp: int64(i + 1),
		})
	}
	groups := []db.GroupCursor{{ChatJID: "g1@g.us", SinceTimestamp: 0, LastBotMsgID: ""}}
	msgs, err := d.GetNewMessages(groups, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Errorf("expected 4 (limited), got %d", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Timestamp < msgs[i-1].Timestamp {
			t.Errorf("messages not in chronological order: %v", msgs)
		}
	}
}

// ---- StoreChatMetadata ----

func TestStoreChatMetadata_DefaultsToJIDName(t *testing.T) {
	d := newTestDB(t)
	if err := d.StoreChatMetadata("chat@g.us", "", true, 1000); err != nil {
		t.Fatal(err)
	}
	chats, err := d.GetAllChats()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range chats {
		if c.JID == "chat@g.us" {
			found = true
			if c.Name != "chat@g.us" {
				t.Errorf("expected name=JID, got %q", c.Name)
			}
		}
	}
	if !found {
		t.Error("chat not found")
	}
}

func TestStoreChatMetadata_WithExplicitName(t *testing.T) {
	d := newTestDB(t)
	if err := d.StoreChatMetadata("chat@g.us", "My Group", true, 1000); err != nil {
		t.Fatal(err)
	}
	chats, err := d.GetAllChats()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c.JID == "chat@g.us" && c.Name != "My Group" {
			t.Errorf("expected 'My Group', got %q", c.Name)
		}
	}
}

func TestStoreChatMetadata_UpdatesName(t *testing.T) {
	d := newTestDB(t)
	d.StoreChatMetadata("chat@g.us", "Old Name", true, 1000)
	d.StoreChatMetadata("chat@g.us", "New Name", true, 2000)
	chats, err := d.GetAllChats()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c.JID == "chat@g.us" && c.Name != "New Name" {
			t.Errorf("expected updated name, got %q", c.Name)
		}
	}
}

func TestStoreChatMetadata_PreservesNewerTimestamp(t *testing.T) {
	d := newTestDB(t)
	d.StoreChatMetadata("chat@g.us", "A", true, 5000)
	d.StoreChatMetadata("chat@g.us", "B", true, 1000) // older; should not overwrite timestamp
	chats, err := d.GetAllChats()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c.JID == "chat@g.us" && c.LastActivity != 5000 {
			t.Errorf("expected timestamp 5000 to be preserved, got %d", c.LastActivity)
		}
	}
}

// ---- Task CRUD ----

func TestTask_CreateRetrieveUpdateDelete(t *testing.T) {
	d := newTestDB(t)
	task := types.ScheduledTask{
		GroupFolder:   "/groups/main",
		Prompt:        "say hello",
		ScheduleType:  types.ScheduleInterval,
		ScheduleValue: "3600",
		Status:        types.TaskStatusActive,
		NextRun:       9999,
	}
	id, err := d.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := d.GetTaskByID(id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Prompt != "say hello" || got.Status != types.TaskStatusActive {
		t.Errorf("unexpected task: %+v", got)
	}

	got.Status = types.TaskStatusPaused
	if err := d.UpdateTask(*got); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	got2, _ := d.GetTaskByID(id)
	if got2.Status != types.TaskStatusPaused {
		t.Errorf("expected paused status, got %v", got2.Status)
	}

	if err := d.DeleteTask(id); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	_, err = d.GetTaskByID(id)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

// ---- RegisteredGroup isMain ----

func TestRegisteredGroup_IsMainRoundTrip(t *testing.T) {
	d := newTestDB(t)
	g := types.RegisteredGroup{
		Name:    "main",
		Folder:  "/groups/main",
		Trigger: "",
		IsMain:  true,
	}
	if err := d.SetRegisteredGroup("main@g.us", g); err != nil {
		t.Fatalf("SetRegisteredGroup: %v", err)
	}
	got, err := d.GetRegisteredGroup("main@g.us")
	if err != nil {
		t.Fatalf("GetRegisteredGroup: %v", err)
	}
	if !got.IsMain {
		t.Error("expected IsMain=true after round-trip")
	}
}

func TestRegisteredGroup_NonMainOmitsIsMain(t *testing.T) {
	d := newTestDB(t)
	g := types.RegisteredGroup{
		Name:    "sub",
		Folder:  "/groups/sub",
		Trigger: "!sub",
		IsMain:  false,
	}
	if err := d.SetRegisteredGroup("sub@g.us", g); err != nil {
		t.Fatalf("SetRegisteredGroup: %v", err)
	}
	got, err := d.GetRegisteredGroup("sub@g.us")
	if err != nil {
		t.Fatalf("GetRegisteredGroup: %v", err)
	}
	if got.IsMain {
		t.Error("expected IsMain=false for non-main group")
	}
}
