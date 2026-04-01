// Package ipc implements the host-side IPC watcher.
//
// The agent container writes JSON files into per-group IPC directories:
//
//	groups/<folder>/.ipc/messages/  — send_message payloads
//	groups/<folder>/.ipc/tasks/     — task operation payloads
//
// The watcher polls those directories, processes each file, and deletes it on
// success. Failed files are moved to groups/<folder>/.ipc/errors/ for debugging.
//
// After any change to the task set the watcher rewrites
// groups/<folder>/.ipc/current_tasks.json so the container's list_tasks tool
// can read the current state.
package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jonsampson/gopherclaw/internal/db"
	"github.com/jonsampson/gopherclaw/internal/scheduler"
	"github.com/jonsampson/gopherclaw/internal/types"
)

// Watcher polls IPC directories for all registered groups.
type Watcher struct {
	groupsDir string
	poll      time.Duration
	database  *db.DB
	channel   types.Channel
}

// New returns a Watcher that reads IPC files from groupsDir/<folder>/.ipc/.
func New(groupsDir string, poll time.Duration, database *db.DB, channel types.Channel) *Watcher {
	return &Watcher{
		groupsDir: groupsDir,
		poll:      poll,
		database:  database,
		channel:   channel,
	}
}

// Start runs the watcher loop until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick()
		}
	}
}

func (w *Watcher) tick() {
	groups, err := w.database.GetAllRegisteredGroups()
	if err != nil {
		log.Printf("ipc: GetAllRegisteredGroups: %v", err)
		return
	}
	for _, g := range groups {
		ipcDir := filepath.Join(w.groupsDir, g.Folder, ".ipc")
		w.processDir(filepath.Join(ipcDir, "messages"), w.processMessage, g)
		w.processDir(filepath.Join(ipcDir, "tasks"), w.processTask, g)
	}
}

// processDir reads all *.json files in dir, calls handler for each, then
// deletes successfully processed files or moves failures to errors/.
func (w *Watcher) processDir(dir string, handler func([]byte, types.RegisteredGroup) error, g types.RegisteredGroup) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("ipc: ReadDir %s: %v", dir, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("ipc: ReadFile %s: %v", path, err)
			continue
		}
		if err := handler(data, g); err != nil {
			log.Printf("ipc: process %s: %v", path, err)
			w.moveToErrors(path, g.Folder)
			continue
		}
		_ = os.Remove(path)
	}
}

func (w *Watcher) moveToErrors(path, groupFolder string) {
	errDir := filepath.Join(w.groupsDir, groupFolder, ".ipc", "errors")
	if err := os.MkdirAll(errDir, 0o755); err != nil {
		log.Printf("ipc: mkdir errors: %v", err)
		return
	}
	dst := filepath.Join(errDir, filepath.Base(path))
	if err := os.Rename(path, dst); err != nil {
		log.Printf("ipc: move to errors: %v", err)
	}
}

// ---------- Message processing ----------

type msgPayload struct {
	Type    string `json:"type"`
	ChatJID string `json:"chatJid"`
	Text    string `json:"text"`
}

func (w *Watcher) processMessage(data []byte, g types.RegisteredGroup) error {
	var p msgPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if p.Type != "message" {
		return fmt.Errorf("unexpected type %q in messages dir", p.Type)
	}
	if p.Text == "" {
		return fmt.Errorf("empty text")
	}
	jid := p.ChatJID
	if jid == "" {
		jid = g.Folder // fall back to folder name as JID placeholder
	}
	return w.channel.SendMessage(jid, p.Text)
}

// ---------- Task processing ----------

type taskPayload struct {
	Type          string `json:"type"`
	TaskID        string `json:"taskId"`
	Prompt        string `json:"prompt"`
	ScheduleType  string `json:"schedule_type"`
	ScheduleValue string `json:"schedule_value"`
	GroupFolder   string `json:"groupFolder"`
	IsMain        bool   `json:"isMain"`
}

func (w *Watcher) processTask(data []byte, g types.RegisteredGroup) error {
	var p taskPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	switch p.Type {
	case "schedule_task":
		return w.handleScheduleTask(p, g)
	case "pause_task":
		return w.handleSetTaskStatus(p, g, types.TaskStatusPaused)
	case "resume_task":
		return w.handleSetTaskStatus(p, g, types.TaskStatusActive)
	case "cancel_task":
		return w.handleCancelTask(p, g)
	default:
		return fmt.Errorf("unknown task op type %q", p.Type)
	}
}

func (w *Watcher) handleScheduleTask(p taskPayload, g types.RegisteredGroup) error {
	if p.Prompt == "" || p.ScheduleType == "" {
		return fmt.Errorf("schedule_task missing required fields")
	}
	now := time.Now()
	task := types.ScheduledTask{
		GroupFolder:   g.Folder,
		Prompt:        p.Prompt,
		ScheduleType:  types.ScheduleType(p.ScheduleType),
		ScheduleValue: p.ScheduleValue,
		Status:        types.TaskStatusActive,
		NextRun:       now.Unix(),
	}
	// Compute first next_run so the scheduler doesn't fire immediately.
	if next := scheduler.ComputeNextRun(task, now); next != nil {
		task.NextRun = next.Unix()
	}
	id, err := w.database.CreateTask(task)
	if err != nil {
		return fmt.Errorf("CreateTask: %w", err)
	}
	log.Printf("ipc: created task %d for group %s", id, g.Folder)
	w.writeCurrentTasks(g.Folder)
	return nil
}

func (w *Watcher) handleSetTaskStatus(p taskPayload, g types.RegisteredGroup, status types.TaskStatus) error {
	id, err := parseTaskID(p.TaskID)
	if err != nil {
		return err
	}
	task, err := w.database.GetTaskByID(id)
	if err != nil {
		return fmt.Errorf("GetTaskByID: %w", err)
	}
	if !g.IsMain && task.GroupFolder != g.Folder {
		return fmt.Errorf("task %d belongs to a different group", id)
	}
	task.Status = status
	if err := w.database.UpdateTask(*task); err != nil {
		return fmt.Errorf("UpdateTask: %w", err)
	}
	w.writeCurrentTasks(g.Folder)
	return nil
}

func (w *Watcher) handleCancelTask(p taskPayload, g types.RegisteredGroup) error {
	id, err := parseTaskID(p.TaskID)
	if err != nil {
		return err
	}
	task, err := w.database.GetTaskByID(id)
	if err != nil {
		return fmt.Errorf("GetTaskByID: %w", err)
	}
	if !g.IsMain && task.GroupFolder != g.Folder {
		return fmt.Errorf("task %d belongs to a different group", id)
	}
	if err := w.database.DeleteTask(id); err != nil {
		return fmt.Errorf("DeleteTask: %w", err)
	}
	log.Printf("ipc: cancelled task %d", id)
	w.writeCurrentTasks(g.Folder)
	return nil
}

// writeCurrentTasks writes a snapshot of the group's visible tasks to
// groups/<folder>/.ipc/current_tasks.json for the list_tasks tool.
func (w *Watcher) writeCurrentTasks(groupFolder string) {
	all, err := w.database.GetAllTasks()
	if err != nil {
		log.Printf("ipc: GetAllTasks for %s: %v", groupFolder, err)
		return
	}

	isMain := false
	groups, _ := w.database.GetAllRegisteredGroups()
	for _, g := range groups {
		if g.Folder == groupFolder && g.IsMain {
			isMain = true
			break
		}
	}

	var visible []types.ScheduledTask
	for _, t := range all {
		if isMain || t.GroupFolder == groupFolder {
			visible = append(visible, t)
		}
	}

	data, err := json.MarshalIndent(map[string]interface{}{"tasks": visible}, "", "  ")
	if err != nil {
		log.Printf("ipc: marshal current_tasks: %v", err)
		return
	}
	ipcDir := filepath.Join(w.groupsDir, groupFolder, ".ipc")
	if err := os.MkdirAll(ipcDir, 0o755); err != nil {
		log.Printf("ipc: mkdir %s: %v", ipcDir, err)
		return
	}
	tmp := filepath.Join(ipcDir, ".current_tasks.json.tmp")
	dst := filepath.Join(ipcDir, "current_tasks.json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("ipc: write current_tasks tmp: %v", err)
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		log.Printf("ipc: rename current_tasks: %v", err)
	}
}

func parseTaskID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid task_id %q: %w", s, err)
	}
	return id, nil
}
