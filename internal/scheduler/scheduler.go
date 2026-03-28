// Package scheduler provides task scheduling with drift prevention,
// mirroring nanoclaw's task-scheduler semantics.
package scheduler

import (
	"fmt"
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
