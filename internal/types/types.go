package types

// NewMessage represents an inbound message from a messaging channel.
type NewMessage struct {
	ID          string
	ChatJID     string
	Sender      string
	Content     string
	Timestamp   int64
	IsFromMe    bool
	IsBotMessage bool
}

// RegisteredGroup is a chat group configured to be handled by an agent.
type RegisteredGroup struct {
	Name    string
	Folder  string
	Trigger string
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
	ScheduleValue string
	Status        TaskStatus
	NextRun       int64 // Unix timestamp (seconds)
}

// TaskRunLog records the outcome of a single task execution.
type TaskRunLog struct {
	TaskID  int64
	RanAt   int64
	Status  string
	Output  string
}

// Channel is the interface that messaging platform adapters must implement.
type Channel interface {
	Connect() error
	Disconnect() error
	SendMessage(chatJID, text string) error
}

// OnInboundMessage is called when a new message arrives from a channel.
type OnInboundMessage func(chatJID string, msg NewMessage)

// OnChatMetadata is called when chat name/group metadata is discovered.
type OnChatMetadata func(chatJID, name string, isGroup bool, channel Channel)

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
	AllowModeTrigger AllowMode = "trigger"
	AllowModeDrop    AllowMode = "drop"
)

// ChatAllowlistConfig overrides the default allowlist for a specific chat.
type ChatAllowlistConfig struct {
	Allow     interface{} // "*" or []string
	Mode      AllowMode
	LogDenied *bool
}

// AllowlistConfig is the top-level sender allowlist configuration.
type AllowlistConfig struct {
	Allow     interface{} // "*" or []string
	Mode      AllowMode
	LogDenied bool
	PerChat   map[string]ChatAllowlistConfig
}

// ContainerInput is the configuration passed to a container agent invocation.
type ContainerInput struct {
	Prompt        string
	SessionID     string
	GroupFolder   string
	ChatJID       string
	IsMain        bool
	IsScheduledTask bool
	AssistantName string
	Script        string
}

// ContainerStatus indicates success or failure of a container run.
type ContainerStatus string

const (
	ContainerStatusSuccess ContainerStatus = "success"
	ContainerStatusError   ContainerStatus = "error"
)

// ContainerOutput is the result returned by a container agent.
type ContainerOutput struct {
	Status     ContainerStatus
	Result     *string
	NewSessionID string
	Error      string
}
