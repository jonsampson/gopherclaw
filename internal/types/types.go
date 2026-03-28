// Package types defines the core data structures shared across gopherclaw packages.
package types

// NewMessage represents an inbound message from a messaging channel.
type NewMessage struct {
	ID        string
	ChatJID   string
	Sender    string
	Content   string
	Timestamp int64
	IsFromMe  bool // true for messages sent by the bot itself
}

// RegisteredGroup is a chat group configured to be handled by an agent.
type RegisteredGroup struct {
	JID     string // unique chat identifier from the messaging platform
	Name    string
	Folder  string
	Trigger string // pattern that must prefix messages for non-main groups
	IsMain  bool
}

// ScheduleType controls how a task recurs.
type ScheduleType string

const (
	ScheduleOnce     ScheduleType = "once"
	ScheduleInterval ScheduleType = "interval"
	ScheduleCron     ScheduleType = "cron"
)

// TaskStatus represents the lifecycle state of a scheduled task.
type TaskStatus string

const (
	TaskStatusActive TaskStatus = "active"
	TaskStatusPaused TaskStatus = "paused"
	TaskStatusDone   TaskStatus = "done"
)

// ScheduledTask is a recurring or one-time agent invocation.
type ScheduledTask struct {
	ID            int64
	GroupFolder   string
	Prompt        string
	ScheduleType  ScheduleType
	ScheduleValue string // interval in seconds, or cron expression
	Status        TaskStatus
	NextRun       int64 // Unix timestamp (seconds)
}

// TaskRunLog records the outcome of a single task execution.
type TaskRunLog struct {
	TaskID int64
	RanAt  int64
	Status string
	Output string
}

// Sender is the minimal interface for delivering a message to a chat.
// Use this where only outbound delivery is needed (e.g. processGroup).
type Sender interface {
	SendMessage(chatJID, text string) error
}

// Channel is a full messaging platform adapter: manages the connection
// lifecycle and can send messages. Inbound messages arrive via the
// OnInboundMessage callback, not through this interface.
type Channel interface {
	Sender
	Connect() error
	Disconnect() error
}

// OnInboundMessage is called when a new message arrives from a channel.
type OnInboundMessage func(chatJID string, msg NewMessage)

// OnChatMetadata is called when chat name/group metadata is discovered.
// The sender argument may be used to deliver an acknowledgement.
type OnChatMetadata func(chatJID, name string, isGroup bool, sender Sender)

// ChatInfo holds metadata about a known chat.
type ChatInfo struct {
	JID          string
	Name         string
	LastActivity int64
	IsGroup      bool
}

// AvailableGroup is a snapshot of a group visible to the agent.
type AvailableGroup struct {
	JID          string
	Name         string
	LastActivity int64
	IsRegistered bool
}

// AllowMode controls how non-allowed senders are handled.
type AllowMode string

const (
	// AllowModeTrigger means unallowed senders' messages are not processed
	// but are still stored (they can trigger on-demand if queried directly).
	AllowModeTrigger AllowMode = "trigger"
	// AllowModeDrop means unallowed senders' messages are silently discarded.
	AllowModeDrop AllowMode = "drop"
)

// AllowRule represents the set of senders permitted to interact.
// Use AllowEveryone or AllowOnly to construct values.
// The zero value permits nobody.
type AllowRule struct {
	wildcard bool
	list     []string
}

// AllowEveryone returns an AllowRule that permits all senders (the "*" wildcard).
func AllowEveryone() AllowRule { return AllowRule{wildcard: true} }

// AllowOnly returns an AllowRule that permits only the named senders.
// Passing nil or an empty slice permits nobody.
func AllowOnly(senders []string) AllowRule { return AllowRule{list: senders} }

// Allows reports whether sender is permitted by this rule.
func (r AllowRule) Allows(sender string) bool {
	if r.wildcard {
		return true
	}
	for _, s := range r.list {
		if s == sender {
			return true
		}
	}
	return false
}

// IsWildcard reports whether this rule allows all senders.
func (r AllowRule) IsWildcard() bool { return r.wildcard }

// List returns the explicit sender list. Returns nil for wildcard rules.
func (r AllowRule) List() []string { return r.list }

// ChatAllowlistConfig overrides the default allowlist for a specific chat.
type ChatAllowlistConfig struct {
	Allow     AllowRule
	Mode      AllowMode
	LogDenied *bool // nil means inherit from the top-level AllowlistConfig
}

// AllowlistConfig is the top-level sender allowlist configuration.
type AllowlistConfig struct {
	Allow     AllowRule
	Mode      AllowMode
	LogDenied bool
	PerChat   map[string]ChatAllowlistConfig // keyed by chat JID
}

// ContainerInput is the configuration passed to a container agent invocation.
type ContainerInput struct {
	Prompt          string
	SessionID       string
	GroupFolder     string
	ChatJID         string
	IsMain          bool
	IsScheduledTask bool
	AssistantName   string
	Script          string // shell script to execute; populated in tests / lightweight mode
}

// ContainerStatus indicates success or failure of a container run.
type ContainerStatus string

const (
	ContainerStatusSuccess ContainerStatus = "success"
	ContainerStatusError   ContainerStatus = "error"
)

// ContainerOutput is the result returned by a container agent.
type ContainerOutput struct {
	Status       ContainerStatus
	Result       *string // captured output text; nil on error or when no output markers found
	NewSessionID string
	Error        string // non-empty when Status == ContainerStatusError
}
