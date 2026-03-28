// Package scheduler_test exercises ComputeNextRun, ValidateGroupFolder,
// StartSchedulerLoop, and the internal runOnce helper (via an exported shim).
package scheduler_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jonsampson/gopherclaw/internal/scheduler"
	"github.com/jonsampson/gopherclaw/internal/types"
)

// ---- fakeStore ----

// fakeStore is an in-memory TaskStore used to test runOnce without SQLite.
type fakeStore struct {
	tasks   []types.ScheduledTask
	updated []types.ScheduledTask
	getErr  error
	setErr  error
}

func (f *fakeStore) GetDueTasks(now int64) ([]types.ScheduledTask, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	var due []types.ScheduledTask
	for _, t := range f.tasks {
		if t.Status == types.TaskStatusActive && t.NextRun <= now {
			due = append(due, t)
		}
	}
	return due, nil
}

func (f *fakeStore) UpdateTask(task types.ScheduledTask) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.updated = append(f.updated, task)
	return nil
}

// ---- runOnce tests (via RunOnce shim) ----

// ---- ComputeNextRun ----

func TestComputeNextRun_OnceTasks_ReturnNil(t *testing.T) {
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleOnce,
		ScheduleValue: "",
		NextRun:       1000,
	}
	if got := scheduler.ComputeNextRun(task, time.Unix(2000, 0)); got != nil {
		t.Errorf("once task: expected nil, got %v", *got)
	}
}

func TestComputeNextRun_IntervalDriftPrevention(t *testing.T) {
	// Task is scheduled every 3600 s; previous NextRun was at t=10000.
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleInterval,
		ScheduleValue: "3600",
		NextRun:       10000,
	}
	// "now" is just past the scheduled time — the next run must be
	// previousNextRun + 1×interval, NOT now + interval.
	now := time.Unix(10005, 0)
	next := scheduler.ComputeNextRun(task, now)
	if next == nil {
		t.Fatal("expected non-nil next run for interval task")
	}
	expected := task.NextRun + 3600
	if next.Unix() != expected {
		t.Errorf("drift prevention: expected %d, got %d", expected, next.Unix())
	}
}

func TestComputeNextRun_MissedIntervalsSkipWithoutLooping(t *testing.T) {
	const intervalSec = int64(60)
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleInterval,
		ScheduleValue: "60",
		NextRun:       1000,
	}
	// Way in the future — many intervals were missed.
	now := time.Unix(5000, 0)
	next := scheduler.ComputeNextRun(task, now)
	if next == nil {
		t.Fatal("expected non-nil next run")
	}
	if !next.After(now) {
		t.Errorf("next run %v should be after now %v", *next, now)
	}
	// Must align to the original schedule grid: (next - base) % interval == 0.
	offset := (next.Unix() - task.NextRun) % intervalSec
	if offset != 0 {
		t.Errorf("next run not on schedule grid (base=%d, interval=%d, next=%d, offset=%d)",
			task.NextRun, intervalSec, next.Unix(), offset)
	}
}

func TestComputeNextRun_IntervalZero_ReturnsNil(t *testing.T) {
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleInterval,
		ScheduleValue: "0",
		NextRun:       1000,
	}
	if got := scheduler.ComputeNextRun(task, time.Unix(2000, 0)); got != nil {
		t.Errorf("interval=0: expected nil, got %v", *got)
	}
}

func TestComputeNextRun_IntervalInvalidValue_ReturnsNil(t *testing.T) {
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleInterval,
		ScheduleValue: "not-a-number",
		NextRun:       1000,
	}
	if got := scheduler.ComputeNextRun(task, time.Unix(2000, 0)); got != nil {
		t.Errorf("invalid interval: expected nil, got %v", *got)
	}
}

func TestComputeNextRun_CronSchedule(t *testing.T) {
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleCron,
		ScheduleValue: "0 * * * *", // top of every hour
	}
	now := time.Unix(1_000_000, 0)
	next := scheduler.ComputeNextRun(task, now)
	if next == nil {
		t.Fatal("expected non-nil for valid cron task")
	}
	if !next.After(now) {
		t.Errorf("cron next run %v should be after now %v", *next, now)
	}
}

func TestComputeNextRun_InvalidCronExpression_ReturnsNil(t *testing.T) {
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleCron,
		ScheduleValue: "not a valid cron",
	}
	if got := scheduler.ComputeNextRun(task, time.Now()); got != nil {
		t.Errorf("invalid cron: expected nil, got %v", *got)
	}
}

func TestComputeNextRun_UnknownScheduleType_ReturnsNil(t *testing.T) {
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleType("unknown"),
		ScheduleValue: "whatever",
		NextRun:       1000,
	}
	if got := scheduler.ComputeNextRun(task, time.Now()); got != nil {
		t.Errorf("unknown type: expected nil, got %v", *got)
	}
}

// ---- ValidateGroupFolder ----

func TestValidateGroupFolder_RejectsPathTraversal(t *testing.T) {
	if err := scheduler.ValidateGroupFolder("../../outside"); err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestValidateGroupFolder_RejectsTraversalInMiddle(t *testing.T) {
	if err := scheduler.ValidateGroupFolder("groups/../../../etc/passwd"); err == nil {
		t.Error("expected error for traversal in the middle of path")
	}
}

func TestValidateGroupFolder_AcceptsAbsolutePath(t *testing.T) {
	if err := scheduler.ValidateGroupFolder("/groups/main"); err != nil {
		t.Errorf("unexpected error for absolute path: %v", err)
	}
}

func TestValidateGroupFolder_AcceptsSimpleRelativePath(t *testing.T) {
	if err := scheduler.ValidateGroupFolder("groups/main"); err != nil {
		t.Errorf("unexpected error for simple relative path: %v", err)
	}
}

func TestValidateGroupFolder_AcceptsCurrentDirRelative(t *testing.T) {
	if err := scheduler.ValidateGroupFolder("main"); err != nil {
		t.Errorf("unexpected error for single-component path: %v", err)
	}
}

// ---- RunOnce (scheduler loop tick) ----

func TestRunOnce_NoDueTasks(t *testing.T) {
	store := &fakeStore{
		tasks: []types.ScheduledTask{
			{ID: 1, Status: types.TaskStatusActive, ScheduleType: types.ScheduleInterval, ScheduleValue: "60", NextRun: 9999},
		},
	}
	var called int
	scheduler.RunOnce(store, func(types.ScheduledTask) { called++ }, time.Unix(1000, 0))
	if called != 0 {
		t.Errorf("expected 0 enqueues, got %d", called)
	}
	if len(store.updated) != 0 {
		t.Errorf("expected 0 updates, got %d", len(store.updated))
	}
}

func TestRunOnce_DueIntervalTask_AdvancesNextRun(t *testing.T) {
	now := time.Unix(5000, 0)
	store := &fakeStore{
		tasks: []types.ScheduledTask{
			{ID: 1, Status: types.TaskStatusActive, ScheduleType: types.ScheduleInterval, ScheduleValue: "3600", NextRun: 1000},
		},
	}
	var enqueued []types.ScheduledTask
	scheduler.RunOnce(store, func(t types.ScheduledTask) { enqueued = append(enqueued, t) }, now)

	if len(enqueued) != 1 {
		t.Fatalf("expected 1 enqueue, got %d", len(enqueued))
	}
	if len(store.updated) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.updated))
	}
	got := store.updated[0]
	if got.Status != types.TaskStatusActive {
		t.Errorf("expected status Active, got %v", got.Status)
	}
	if got.NextRun <= now.Unix() {
		t.Errorf("expected NextRun > now (%d), got %d", now.Unix(), got.NextRun)
	}
}

func TestRunOnce_DueOnceTask_MarkedDone(t *testing.T) {
	now := time.Unix(5000, 0)
	store := &fakeStore{
		tasks: []types.ScheduledTask{
			{ID: 2, Status: types.TaskStatusActive, ScheduleType: types.ScheduleOnce, NextRun: 1000},
		},
	}
	var called int
	scheduler.RunOnce(store, func(types.ScheduledTask) { called++ }, now)

	if called != 1 {
		t.Fatalf("expected 1 enqueue, got %d", called)
	}
	if len(store.updated) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.updated))
	}
	if store.updated[0].Status != types.TaskStatusDone {
		t.Errorf("expected status Done, got %v", store.updated[0].Status)
	}
}

func TestRunOnce_DueCronTask_AdvancesNextRun(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	store := &fakeStore{
		tasks: []types.ScheduledTask{
			{ID: 3, Status: types.TaskStatusActive, ScheduleType: types.ScheduleCron, ScheduleValue: "0 * * * *", NextRun: 1},
		},
	}
	var called int
	scheduler.RunOnce(store, func(types.ScheduledTask) { called++ }, now)

	if called != 1 {
		t.Fatalf("expected 1 enqueue, got %d", called)
	}
	if len(store.updated) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.updated))
	}
	if store.updated[0].NextRun <= now.Unix() {
		t.Errorf("expected NextRun > now, got %d", store.updated[0].NextRun)
	}
}

func TestRunOnce_GetDueTasksError_DoesNotPanic(t *testing.T) {
	store := &fakeStore{getErr: fmt.Errorf("db failure")}
	var called int
	// Must not panic; errors are logged.
	scheduler.RunOnce(store, func(types.ScheduledTask) { called++ }, time.Now())
	if called != 0 {
		t.Errorf("expected 0 enqueues on DB error, got %d", called)
	}
}

func TestRunOnce_UpdateTaskError_ContinuesOtherTasks(t *testing.T) {
	now := time.Unix(5000, 0)
	store := &fakeStore{
		tasks: []types.ScheduledTask{
			{ID: 1, Status: types.TaskStatusActive, ScheduleType: types.ScheduleOnce, NextRun: 1},
			{ID: 2, Status: types.TaskStatusActive, ScheduleType: types.ScheduleOnce, NextRun: 1},
		},
		setErr: fmt.Errorf("write failure"),
	}
	var called int
	// Both tasks should be enqueued even though UpdateTask always errors.
	scheduler.RunOnce(store, func(types.ScheduledTask) { called++ }, now)
	if called != 2 {
		t.Errorf("expected both tasks enqueued despite update errors, got %d", called)
	}
}

// ---- StartSchedulerLoop ----

func TestStartSchedulerLoop_CancelsCleanly(t *testing.T) {
	store := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scheduler.StartSchedulerLoop(ctx, store, func(types.ScheduledTask) {}, 10*time.Millisecond)
	}()

	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		// OK — loop exited promptly after cancel.
	case <-time.After(500 * time.Millisecond):
		t.Error("StartSchedulerLoop did not exit within 500ms after context cancel")
	}
}
