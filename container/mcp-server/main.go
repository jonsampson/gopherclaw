// Command gopherclaw-mcp is an MCP server that runs inside the agent container.
// It implements the Model Context Protocol over stdio (newline-delimited JSON-RPC 2.0)
// and exposes tools that let the claude agent interact with the gopherclaw host:
// sending messages to chats and managing scheduled tasks via IPC files.
//
// The host-side internal/ipc watcher reads those files and acts on them.
//
// Environment variables (set by the container entrypoint via settings.json):
//
//	GOPHERCLAW_IPC_DIR      path to the per-group /workspace/ipc directory
//	GOPHERCLAW_CHAT_JID     JID of the current group chat
//	GOPHERCLAW_GROUP_FOLDER folder name of the current group
//	GOPHERCLAW_IS_MAIN      "1" if this is the main group, "0" otherwise
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// ---------- JSON-RPC 2.0 ----------

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // nil → notification (no response)
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---------- MCP types ----------

type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string             `json:"type"`
	Properties map[string]propDef `json:"properties"`
	Required   []string           `json:"required,omitempty"`
}

type propDef struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type toolCallResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ---------- IPC file payloads (read by internal/ipc on the host) ----------

type ipcSendMessage struct {
	Type        string `json:"type"`
	ChatJID     string `json:"chatJid"`
	Text        string `json:"text"`
	Sender      string `json:"sender,omitempty"`
	GroupFolder string `json:"groupFolder"`
	Timestamp   string `json:"timestamp"`
}

type ipcScheduleTask struct {
	Type          string `json:"type"`
	Prompt        string `json:"prompt"`
	ScheduleType  string `json:"schedule_type"`
	ScheduleValue string `json:"schedule_value"`
	ContextMode   string `json:"context_mode"`
	GroupFolder   string `json:"groupFolder"`
	IsMain        bool   `json:"isMain"`
	Timestamp     string `json:"timestamp"`
}

type ipcTaskOp struct {
	Type        string `json:"type"` // pause_task | resume_task | cancel_task
	TaskID      string `json:"taskId"`
	GroupFolder string `json:"groupFolder"`
	IsMain      bool   `json:"isMain"`
	Timestamp   string `json:"timestamp"`
}

// ---------- Tool definitions ----------

var tools = []toolDef{
	{
		Name:        "send_message",
		Description: "Send a message to the current group chat.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]propDef{
				"text":   {Type: "string", Description: "Message text to send."},
				"sender": {Type: "string", Description: "Optional sender identity shown in the message."},
			},
			Required: []string{"text"},
		},
	},
	{
		Name:        "schedule_task",
		Description: "Schedule a recurring or one-time agent task for this group.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]propDef{
				"prompt":         {Type: "string", Description: "Instructions for the agent when the task runs."},
				"schedule_type":  {Type: "string", Description: `"cron", "interval", or "once".`},
				"schedule_value": {Type: "string", Description: "Cron expression, interval in seconds, or ISO timestamp."},
				"context_mode":   {Type: "string", Description: `"group" to include chat history or "isolated" for a fresh session.`},
			},
			Required: []string{"prompt", "schedule_type", "schedule_value", "context_mode"},
		},
	},
	{
		Name:        "list_tasks",
		Description: "List scheduled tasks visible to this group (reads current_tasks.json written by the host).",
		InputSchema: inputSchema{Type: "object", Properties: map[string]propDef{}},
	},
	{
		Name:        "pause_task",
		Description: "Pause a scheduled task by ID.",
		InputSchema: inputSchema{
			Type:       "object",
			Properties: map[string]propDef{"task_id": {Type: "string", Description: "Task ID to pause."}},
			Required:   []string{"task_id"},
		},
	},
	{
		Name:        "resume_task",
		Description: "Resume a paused task by ID.",
		InputSchema: inputSchema{
			Type:       "object",
			Properties: map[string]propDef{"task_id": {Type: "string", Description: "Task ID to resume."}},
			Required:   []string{"task_id"},
		},
	},
	{
		Name:        "cancel_task",
		Description: "Cancel (delete) a scheduled task by ID.",
		InputSchema: inputSchema{
			Type:       "object",
			Properties: map[string]propDef{"task_id": {Type: "string", Description: "Task ID to cancel."}},
			Required:   []string{"task_id"},
		},
	},
}

// ---------- Server state ----------

var (
	ipcDir      = os.Getenv("GOPHERCLAW_IPC_DIR")
	chatJID     = os.Getenv("GOPHERCLAW_CHAT_JID")
	groupFolder = os.Getenv("GOPHERCLAW_GROUP_FOLDER")
	isMain      = os.Getenv("GOPHERCLAW_IS_MAIN") == "1"
)

// ---------- Main loop ----------

func main() {
	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	// Increase scanner buffer for large prompts.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg rpcMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			fmt.Fprintf(os.Stderr, "gopherclaw-mcp: parse error: %v\n", err)
			continue
		}

		// Notifications have no ID field — do not respond.
		if msg.ID == nil {
			continue
		}

		resp := dispatch(msg)
		resp.JSONRPC = "2.0"
		resp.ID = msg.ID
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(os.Stderr, "gopherclaw-mcp: encode error: %v\n", err)
		}
	}
}

func dispatch(msg rpcMsg) rpcMsg {
	switch msg.Method {
	case "initialize":
		return rpcMsg{Result: handleInitialize(msg.Params)}
	case "tools/list":
		return rpcMsg{Result: map[string]interface{}{"tools": tools}}
	case "tools/call":
		return rpcMsg{Result: handleToolCall(msg.Params)}
	default:
		return rpcMsg{Error: &rpcError{Code: -32601, Message: "method not found"}}
	}
}

func handleInitialize(params json.RawMessage) interface{} {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	ver := p.ProtocolVersion
	if ver == "" {
		ver = "2024-11-05"
	}
	return map[string]interface{}{
		"protocolVersion": ver,
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		"serverInfo":      map[string]interface{}{"name": "gopherclaw-mcp", "version": "1.0.0"},
	}
}

func handleToolCall(params json.RawMessage) toolCallResult {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}

	switch p.Name {
	case "send_message":
		return toolSendMessage(p.Arguments)
	case "schedule_task":
		return toolScheduleTask(p.Arguments)
	case "list_tasks":
		return toolListTasks()
	case "pause_task":
		return toolTaskOp("pause_task", p.Arguments)
	case "resume_task":
		return toolTaskOp("resume_task", p.Arguments)
	case "cancel_task":
		return toolTaskOp("cancel_task", p.Arguments)
	default:
		return toolErr("unknown tool: " + p.Name)
	}
}

// ---------- Tool implementations ----------

func toolSendMessage(args json.RawMessage) toolCallResult {
	var a struct {
		Text   string `json:"text"`
		Sender string `json:"sender"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Text == "" {
		return toolErr("send_message requires non-empty text")
	}
	payload := ipcSendMessage{
		Type:        "message",
		ChatJID:     chatJID,
		Text:        a.Text,
		Sender:      a.Sender,
		GroupFolder: groupFolder,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeIPC(filepath.Join(ipcDir, "messages"), payload); err != nil {
		return toolErr("failed to queue message: " + err.Error())
	}
	return toolOK("Message queued.")
}

func toolScheduleTask(args json.RawMessage) toolCallResult {
	var a struct {
		Prompt        string `json:"prompt"`
		ScheduleType  string `json:"schedule_type"`
		ScheduleValue string `json:"schedule_value"`
		ContextMode   string `json:"context_mode"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return toolErr("invalid arguments: " + err.Error())
	}
	if a.Prompt == "" || a.ScheduleType == "" || a.ScheduleValue == "" || a.ContextMode == "" {
		return toolErr("schedule_task requires prompt, schedule_type, schedule_value, and context_mode")
	}
	payload := ipcScheduleTask{
		Type:          "schedule_task",
		Prompt:        a.Prompt,
		ScheduleType:  a.ScheduleType,
		ScheduleValue: a.ScheduleValue,
		ContextMode:   a.ContextMode,
		GroupFolder:   groupFolder,
		IsMain:        isMain,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeIPC(filepath.Join(ipcDir, "tasks"), payload); err != nil {
		return toolErr("failed to queue task: " + err.Error())
	}
	return toolOK("Task scheduled.")
}

func toolListTasks() toolCallResult {
	data, err := os.ReadFile(filepath.Join(ipcDir, "current_tasks.json"))
	if os.IsNotExist(err) {
		return toolOK("No tasks found (host has not written current_tasks.json yet).")
	}
	if err != nil {
		return toolErr("failed to read current_tasks.json: " + err.Error())
	}
	return toolOK(string(data))
}

func toolTaskOp(opType string, args json.RawMessage) toolCallResult {
	var a struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.TaskID == "" {
		return toolErr(opType + " requires task_id")
	}
	payload := ipcTaskOp{
		Type:        opType,
		TaskID:      a.TaskID,
		GroupFolder: groupFolder,
		IsMain:      isMain,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeIPC(filepath.Join(ipcDir, "tasks"), payload); err != nil {
		return toolErr("failed to queue " + opType + ": " + err.Error())
	}
	return toolOK("Done.")
}

// ---------- Helpers ----------

func writeIPC(dir string, payload interface{}) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s.json", time.Now().UnixMilli(), randomString(8))
	tmp := filepath.Join(dir, "."+name+".tmp")
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func randomString(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func toolOK(text string) toolCallResult {
	return toolCallResult{Content: []contentItem{{Type: "text", Text: text}}}
}

func toolErr(text string) toolCallResult {
	return toolCallResult{
		Content: []contentItem{{Type: "text", Text: text}},
		IsError: true,
	}
}
