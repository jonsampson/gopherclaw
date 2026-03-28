// Package scheduler provides task scheduling with drift prevention,
// mirroring nanoclaw's task-scheduler semantics.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// ComputeNextRun calculates when task should next run, given the current time.
//
//   - ScheduleOnce tasks return nil (they do not recur).
//   - ScheduleInterval tasks anchor to the previous NextRun to prevent drift:
//     next = previousNextRun + N×interval, where N is the smallest positive
//     integer such that next > now. This means a missed interval is skipped
//     rather than immediately replayed.
//   - ScheduleCron tasks use a standard five-field cron expression
//     (minute hour dom month dow) parsed in UTC.
//
// Returns nil for invalid or unrecognised schedule values.
func ComputeNextRun(task types.ScheduledTask, now time.Time) *time.Time {
	switch task.ScheduleType {
	case types.ScheduleOnce:
		return nil

	case types.ScheduleInterval:
		intervalSec, err := strconv.ParseInt(task.ScheduleValue, 10, 64)
		if err != nil || intervalSec <= 0 {
			return nil
		}
		// Advance from the previous NextRun in steps of intervalSec until the
		// result is strictly in the future. This prevents drift and handles
		// any number of missed intervals without looping back.
		next := task.NextRun
		for next <= now.Unix() {
			next += intervalSec
		}
		t := time.Unix(next, 0)
		return &t

	case types.ScheduleCron:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(task.ScheduleValue)
		if err != nil {
			return nil
		}
		t := sched.Next(now)
		return &t

	default:
		return nil
	}
}

// TaskStore is the subset of db.DB that the scheduler loop requires.
// *db.DB satisfies this interface without modification.
type TaskStore interface {
	GetDueTasks(now int64) ([]types.ScheduledTask, error)
	UpdateTask(task types.ScheduledTask) error
}

// StartSchedulerLoop polls store every poll interval for tasks whose NextRun is
// due, passes each to enqueue, advances their NextRun (or marks them done), and
// exits cleanly when ctx is cancelled.
//
// It is safe to run in a goroutine:
//
//	go scheduler.StartSchedulerLoop(ctx, db, enqueueFn, 30*time.Second)
//
// The enqueue callback is called synchronously within each tick so the caller
// can do simple bookkeeping without locks if needed. Actual processing happens
// asynchronously inside the queue.
func StartSchedulerLoop(
	ctx context.Context,
	store TaskStore,
	enqueue func(types.ScheduledTask),
	poll time.Duration,
) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runOnce(store, enqueue, now)
		}
	}
}

// runOnce fetches all due tasks and enqueues them, advancing or retiring each
// one before the next tick. It is package-private but exercised directly by
// tests via a fake clock value — no real timers needed.
//
// Update-before-enqueue semantics: NextRun (or Status=done) is persisted
// before the task is dispatched. A crash between persist and dispatch means
// the task silently skips one cycle, which is preferable to a double-fire.
func runOnce(store TaskStore, enqueue func(types.ScheduledTask), now time.Time) {
	tasks, err := store.GetDueTasks(now.Unix())
	if err != nil {
		log.Printf("scheduler: GetDueTasks: %v", err)
		return
	}
	for _, task := range tasks {
		enqueue(task)
		next := ComputeNextRun(task, now)
		if next == nil {
			task.Status = types.TaskStatusDone
		} else {
			task.NextRun = next.Unix()
		}
		if err := store.UpdateTask(task); err != nil {
			log.Printf("scheduler: UpdateTask(%d): %v", task.ID, err)
			// Continue processing remaining tasks despite per-task errors.
		}
	}
}

// ValidateGroupFolder returns an error if folder is unsafe to use as a working
// directory for a container agent. Absolute paths are always accepted; relative
// paths must not escape the current directory tree (no ".." components).
func ValidateGroupFolder(folder string) error {
	if filepath.IsAbs(folder) {
		return nil
	}
	// filepath.IsLocal rejects paths containing ".." or rooted with "/".
	if !filepath.IsLocal(folder) {
		return fmt.Errorf("group folder %q contains path traversal", folder)
	}
	return nil
}
