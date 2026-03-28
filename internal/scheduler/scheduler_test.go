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
	next := scheduler.ComputeNextRun(task, time.Unix(2000, 0))
	if next != nil {
		t.Errorf("expected nil for once task, got %v", next)
	}
}

func TestComputeNextRun_IntervalDriftPrevention(t *testing.T) {
	// scheduled every 3600 seconds; last next_run was at t=10000
	intervalSec := int64(3600)
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleInterval,
		ScheduleValue: "3600",
		NextRun:       10000,
	}
	// "now" is slightly after 10000 (task just ran)
	now := time.Unix(10005, 0)
	next := scheduler.ComputeNextRun(task, now)
	if next == nil {
		t.Fatal("expected non-nil next run for interval task")
	}
	expected := task.NextRun + intervalSec
	if next.Unix() != expected {
		t.Errorf("drift prevention: expected %d, got %d", expected, next.Unix())
	}
}

func TestComputeNextRun_MissedIntervalsSkipWithoutLooping(t *testing.T) {
	intervalSec := int64(60)
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleInterval,
		ScheduleValue: "60",
		NextRun:       1000,
	}
	// Way in the future — missed many intervals
	now := time.Unix(5000, 0)
	next := scheduler.ComputeNextRun(task, now)
	if next == nil {
		t.Fatal("expected non-nil next for interval task")
	}
	// Must be in the future
	if !next.After(now) {
		t.Errorf("next run %v is not after now %v", *next, now)
	}
	// Must align to the schedule grid: (next - base) % interval == 0
	base := task.NextRun
	offset := (next.Unix() - base) % intervalSec
	if offset != 0 {
		t.Errorf("next run %d is not on the schedule grid (base=%d, interval=%d, offset=%d)",
			next.Unix(), base, intervalSec, offset)
	}
}

func TestComputeNextRun_CronSchedule(t *testing.T) {
	task := types.ScheduledTask{
		ScheduleType:  types.ScheduleCron,
		ScheduleValue: "0 * * * *", // top of every hour
		NextRun:       0,
	}
	now := time.Unix(1000, 0) // arbitrary
	next := scheduler.ComputeNextRun(task, now)
	if next == nil {
		t.Fatal("expected non-nil for cron task")
	}
	if !next.After(now) {
		t.Errorf("cron next run %v should be after now %v", *next, now)
	}
}

// ---- ValidateGroupFolder ----

func TestValidateGroupFolder_RejectsPathTraversal(t *testing.T) {
	err := scheduler.ValidateGroupFolder("../../outside")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestValidateGroupFolder_AcceptsAbsolutePath(t *testing.T) {
	err := scheduler.ValidateGroupFolder("/groups/main")
	if err != nil {
		t.Errorf("unexpected error for absolute path: %v", err)
	}
}

func TestValidateGroupFolder_AcceptsSimpleRelativePath(t *testing.T) {
	err := scheduler.ValidateGroupFolder("groups/main")
	if err != nil {
		t.Errorf("unexpected error for simple relative path: %v", err)
	}
}
