// Package scheduler provides task scheduling with drift prevention,
// mirroring nanoclaw's task-scheduler semantics.
package scheduler

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// ComputeNextRun calculates when a task should next run, given the current time.
//
//   - ScheduleOnce tasks return nil (they don't recur).
//   - ScheduleInterval tasks anchor to the previous NextRun to prevent drift:
//     next = previousNextRun + N*interval, where N is the smallest integer
//     such that next > now.
//   - ScheduleCron tasks use a standard cron parser.
func ComputeNextRun(task types.ScheduledTask, now time.Time) *time.Time {
	switch task.ScheduleType {
	case types.ScheduleOnce:
		return nil

	case types.ScheduleInterval:
		intervalSec, err := strconv.ParseInt(task.ScheduleValue, 10, 64)
		if err != nil || intervalSec <= 0 {
			return nil
		}
		// Advance from previous NextRun in steps of intervalSec until we're
		// strictly in the future. This prevents drift and handles missed intervals.
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

// ValidateGroupFolder returns an error if the folder path contains
// directory traversal components (e.g. "../../outside").
func ValidateGroupFolder(folder string) error {
	// filepath.Clean resolves ".." — if it changes the path leading components,
	// flag it as a traversal attempt.
	clean := filepath.Clean(folder)
	if strings.HasPrefix(clean, "..") {
		return fmt.Errorf("group folder %q contains path traversal", folder)
	}
	return nil
}
