// Package scheduler_test exercises ComputeNextRun and ValidateGroupFolder.
package scheduler_test

import (
	"testing"
	"time"

	"github.com/jonsampson/gopherclaw/internal/scheduler"
	"github.com/jonsampson/gopherclaw/internal/types"
)

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
