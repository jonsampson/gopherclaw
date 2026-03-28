package scheduler

import (
	"time"

	"github.com/jonsampson/gopherclaw/internal/types"
)

// RunOnce is a test-only export of runOnce, allowing scheduler_test to call it
// directly with an arbitrary time.Time value — no real timers needed.
func RunOnce(store TaskStore, enqueue func(types.ScheduledTask), now time.Time) {
	runOnce(store, enqueue, now)
}
