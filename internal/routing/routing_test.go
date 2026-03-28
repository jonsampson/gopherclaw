package routing_test

import (
	"testing"

	"github.com/jonsampson/gopherclaw/internal/routing"
	"github.com/jonsampson/gopherclaw/internal/types"
)

// ---- JID format helpers ----

func TestIsGroupJID_WhatsAppGroup(t *testing.T) {
	if !routing.IsGroupJID("12345@g.us") {
		t.Error("expected @g.us JID to be a group")
	}
}

func TestIsDMJID_WhatsAppDM(t *testing.T) {
	if !routing.IsDMJID("15551234567@s.whatsapp.net") {
		t.Error("expected @s.whatsapp.net JID to be a DM")
	}
}

// ---- GetAvailableGroups ----

func chats(entries ...types.ChatInfo) []types.ChatInfo { return entries }

func registered(jids ...string) map[string]bool {
	m := make(map[string]bool, len(jids))
	for _, j := range jids {
		m[j] = true
	}
	return m
}

func TestGetAvailableGroups_FiltersOutDMs(t *testing.T) {
	all := chats(
		types.ChatInfo{JID: "g1@g.us", Name: "Group One", LastActivity: 100, IsGroup: true},
		types.ChatInfo{JID: "g2@g.us", Name: "Group Two", LastActivity: 200, IsGroup: true},
		types.ChatInfo{JID: "15551234@s.whatsapp.net", Name: "Alice DM", LastActivity: 300, IsGroup: false},
	)
	groups := routing.GetAvailableGroups(all, registered())
	if len(groups) != 2 {
		t.Errorf("expected 2 groups (DM filtered), got %d", len(groups))
	}
	for _, g := range groups {
		if !routing.IsGroupJID(g.JID) {
			t.Errorf("DM leaked into results: %v", g.JID)
		}
	}
}

func TestGetAvailableGroups_ExcludesSentinel(t *testing.T) {
	all := chats(
		types.ChatInfo{JID: "__group_sync__", Name: "sentinel", LastActivity: 999, IsGroup: true},
		types.ChatInfo{JID: "real@g.us", Name: "Real Group", LastActivity: 100, IsGroup: true},
	)
	groups := routing.GetAvailableGroups(all, registered())
	if len(groups) != 1 || groups[0].JID != "real@g.us" {
		t.Errorf("sentinel not excluded, got %v", groups)
	}
}

func TestGetAvailableGroups_MarksRegistrationStatus(t *testing.T) {
	all := chats(
		types.ChatInfo{JID: "reg@g.us", Name: "Registered", LastActivity: 100, IsGroup: true},
		types.ChatInfo{JID: "unreg@g.us", Name: "Unregistered", LastActivity: 200, IsGroup: true},
	)
	groups := routing.GetAvailableGroups(all, registered("reg@g.us"))
	regMap := make(map[string]bool)
	for _, g := range groups {
		regMap[g.JID] = g.IsRegistered
	}
	if !regMap["reg@g.us"] {
		t.Error("reg@g.us should be marked registered")
	}
	if regMap["unreg@g.us"] {
		t.Error("unreg@g.us should not be marked registered")
	}
}

func TestGetAvailableGroups_SortsByRecentActivity(t *testing.T) {
	all := chats(
		types.ChatInfo{JID: "old@g.us", Name: "Old", LastActivity: 100, IsGroup: true},
		types.ChatInfo{JID: "newest@g.us", Name: "Newest", LastActivity: 300, IsGroup: true},
		types.ChatInfo{JID: "mid@g.us", Name: "Mid", LastActivity: 200, IsGroup: true},
	)
	groups := routing.GetAvailableGroups(all, registered())
	if len(groups) != 3 {
		t.Fatalf("expected 3, got %d", len(groups))
	}
	if groups[0].JID != "newest@g.us" || groups[1].JID != "mid@g.us" || groups[2].JID != "old@g.us" {
		t.Errorf("wrong sort order: %v", groups)
	}
}

func TestGetAvailableGroups_ExcludesUnknownFormat(t *testing.T) {
	all := chats(
		types.ChatInfo{JID: "weird-format", Name: "Unknown", LastActivity: 100, IsGroup: false},
		types.ChatInfo{JID: "another@example.com", Name: "Not WA", LastActivity: 200, IsGroup: false},
		types.ChatInfo{JID: "real@g.us", Name: "Real", LastActivity: 50, IsGroup: true},
	)
	groups := routing.GetAvailableGroups(all, registered())
	if len(groups) != 1 || groups[0].JID != "real@g.us" {
		t.Errorf("expected only @g.us group, got %v", groups)
	}
}

func TestGetAvailableGroups_EmptyWhenNoChats(t *testing.T) {
	groups := routing.GetAvailableGroups(nil, registered())
	if len(groups) != 0 {
		t.Errorf("expected empty, got %v", groups)
	}
}
