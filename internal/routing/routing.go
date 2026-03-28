package routing

import (
	"sort"
	"strings"

	"github.com/jonsampson/gopherclaw/internal/types"
)

const sentinel = "__group_sync__"

// IsGroupJID reports whether the JID identifies a WhatsApp group.
func IsGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
}

// IsDMJID reports whether the JID identifies a WhatsApp direct message.
func IsDMJID(jid string) bool {
	return strings.HasSuffix(jid, "@s.whatsapp.net")
}

// GetAvailableGroups filters a list of known chats down to real group JIDs
// (excludes DMs, the __group_sync__ sentinel, and unknown formats), annotates
// each with its registration status, and sorts by most-recent activity first.
func GetAvailableGroups(chats []types.ChatInfo, registeredJIDs map[string]bool) []types.AvailableGroup {
	var groups []types.AvailableGroup
	for _, c := range chats {
		if c.JID == sentinel {
			continue
		}
		if !IsGroupJID(c.JID) {
			continue
		}
		groups = append(groups, types.AvailableGroup{
			JID:          c.JID,
			Name:         c.Name,
			LastActivity: c.LastActivity,
			IsRegistered: registeredJIDs[c.JID],
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].LastActivity > groups[j].LastActivity
	})
	return groups
}
