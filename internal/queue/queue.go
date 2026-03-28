// Package queue provides a concurrency-controlled work queue for per-group
// agent container invocations, mirroring nanoclaw's GroupQueue semantics.
package queue

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxRetries = 5

// ProcessFunc is the function called to handle a queued item.
type ProcessFunc func(Item) error

// Item describes a unit of work (either a message batch or a scheduled task).
type Item struct {
	GroupID string
	TaskID  string // non-empty for tasks; used for dedup
	IsTask  bool
}

// groupState tracks in-flight and pending work for one group.
//
// Idle preemption state machine:
//
//	container starts        → idleReady=false, msgSentAfterIdle=false
//	SendMessage called      → msgSentAfterIdle=true,  idleReady=false
//	NotifyIdle, msgSent=true → clear msgSentAfterIdle, no preemption
//	NotifyIdle, msgSent=false → idleReady=true; if taskQueue non-empty → preempt
type groupState struct {
	running          bool
	isTask           bool // true if the currently-running item is a task
	idleReady        bool // true after first NotifyIdle with no intervening message
	msgSentAfterIdle bool // true if SendMessage was called since last NotifyIdle
	retries          int
	queue            []pendingItem
	taskQueue        []pendingItem
	runningTaskIDs   map[string]bool
}

type pendingItem struct {
	item Item
	proc ProcessFunc
}

// GroupQueue manages concurrent agent execution with per-group serialisation
// and a global concurrency limit.
type GroupQueue struct {
	mu          sync.Mutex
	maxGlobal   int
	activeCount int
	retryBase   time.Duration
	closeDir    string
	groups      map[string]*groupState
	shutdown    bool
}

// New creates a GroupQueue.
//   - maxConcurrent: maximum simultaneous container runs across all groups
//   - retryBase:     initial backoff duration (doubles each retry)
//   - reserved:      unused; kept for API parity
func New(maxConcurrent int, retryBase time.Duration, reserved int) *GroupQueue {
	return &GroupQueue{
		maxGlobal: maxConcurrent,
		retryBase: retryBase,
		groups:    make(map[string]*groupState),
	}
}

// SetCloseDir sets the directory under which per-group _close files are written
// for idle preemption.
func (q *GroupQueue) SetCloseDir(dir string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closeDir = dir
}

// Shutdown prevents new enqueues.
func (q *GroupQueue) Shutdown() {
	q.mu.Lock()
	q.shutdown = true
	q.mu.Unlock()
}

// Enqueue adds a message-processing item for groupID.
func (q *GroupQueue) Enqueue(item Item, proc ProcessFunc) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.shutdown {
		return
	}
	gs := q.getOrCreate(item.GroupID)
	gs.queue = append(gs.queue, pendingItem{item: item, proc: proc})
	q.tryStart(item.GroupID, gs)
}

// EnqueueTask adds a task item for groupID. Tasks are prioritised over messages
// and deduplicated by TaskID while the same task is already running or queued.
func (q *GroupQueue) EnqueueTask(item Item, proc ProcessFunc) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.shutdown {
		return
	}
	gs := q.getOrCreate(item.GroupID)

	// Dedup: reject if the same TaskID is already running or pending.
	if item.TaskID != "" {
		if gs.runningTaskIDs[item.TaskID] {
			return
		}
		for _, p := range gs.taskQueue {
			if p.item.TaskID == item.TaskID {
				return
			}
		}
	}

	gs.taskQueue = append(gs.taskQueue, pendingItem{item: item, proc: proc})
	q.tryStart(item.GroupID, gs)
}

// NotifyIdle signals that the running container for groupID has gone idle.
// If the container recently sent a message, the first NotifyIdle call merely
// clears that "active" flag. If the container has been genuinely idle (no
// message since last NotifyIdle), and a task is pending, the container is
// preempted by writing a _close file.
func (q *GroupQueue) NotifyIdle(groupID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	gs := q.groups[groupID]
	if gs == nil || !gs.running {
		return
	}
	if gs.msgSentAfterIdle {
		// Container sent a message after the last idle signal; treat this
		// NotifyIdle as an acknowledgement but do not preempt yet.
		gs.msgSentAfterIdle = false
		return
	}
	gs.idleReady = true
	if len(gs.taskQueue) > 0 {
		q.writeClose(groupID)
	}
}

// SendMessage signals that a message was sent by the container for groupID,
// which resets the idle state. The text parameter is reserved for future use
// (e.g. logging or rate-limiting) and is currently ignored.
// Returns false if the running item is a task; task containers do not send
// messages back to the chat and should not call this method.
func (q *GroupQueue) SendMessage(groupID, _ string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	gs := q.groups[groupID]
	if gs == nil || !gs.running {
		return false
	}
	if gs.isTask {
		return false
	}
	gs.idleReady = false
	gs.msgSentAfterIdle = true
	return true
}

// ---- internal ----

func (q *GroupQueue) getOrCreate(groupID string) *groupState {
	gs := q.groups[groupID]
	if gs == nil {
		gs = &groupState{runningTaskIDs: make(map[string]bool)}
		q.groups[groupID] = gs
	}
	return gs
}

// tryStart launches the next item for groupID if slots are available.
// Must be called with q.mu held.
func (q *GroupQueue) tryStart(groupID string, gs *groupState) {
	if gs.running || q.activeCount >= q.maxGlobal {
		return
	}

	// Tasks have priority over messages.
	var next *pendingItem
	if len(gs.taskQueue) > 0 {
		item := gs.taskQueue[0]
		gs.taskQueue = gs.taskQueue[1:]
		next = &item
		gs.isTask = true
		if item.item.TaskID != "" {
			gs.runningTaskIDs[item.item.TaskID] = true
		}
	} else if len(gs.queue) > 0 {
		item := gs.queue[0]
		gs.queue = gs.queue[1:]
		next = &item
		gs.isTask = false
	}

	if next == nil {
		return
	}

	gs.running = true
	gs.idleReady = false
	gs.msgSentAfterIdle = false
	q.activeCount++

	go q.run(groupID, *next)
}

func (q *GroupQueue) run(groupID string, pi pendingItem) {
	var err error
	retries := 0
	for {
		err = pi.proc(pi.item)
		if err == nil || retries >= maxRetries {
			break
		}
		retries++
		backoff := q.retryBase * time.Duration(1<<(retries-1))
		time.Sleep(backoff)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	gs := q.groups[groupID]
	if gs == nil {
		return
	}
	gs.running = false
	gs.idleReady = false
	gs.msgSentAfterIdle = false
	if pi.item.IsTask && pi.item.TaskID != "" {
		delete(gs.runningTaskIDs, pi.item.TaskID)
	}
	q.activeCount--

	// Start next item for this group first, then scan other groups.
	q.tryStart(groupID, gs)
	for gid, g := range q.groups {
		if gid != groupID && !g.running && (len(g.queue) > 0 || len(g.taskQueue) > 0) {
			q.tryStart(gid, g)
		}
	}
}

func (q *GroupQueue) writeClose(groupID string) {
	if q.closeDir == "" {
		return
	}
	dir := filepath.Join(q.closeDir, groupID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("queue: writeClose: mkdir %s: %v", dir, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "_close"), []byte{}, 0o644); err != nil {
		log.Printf("queue: writeClose: write _close for %s: %v", groupID, err)
	}
}
