package queue_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonsampson/gopherclaw/internal/queue"
)

// makeBlockingProc returns a ProcessFunc that tracks concurrency, blocks until
// the returned unblock channel is closed, then decrements the counter.
func makeBlockingProc(concurrent, maxConcurrent *int32, wg *sync.WaitGroup) (queue.ProcessFunc, chan struct{}) {
	unblock := make(chan struct{})
	proc := func(_ queue.Item) error {
		cur := atomic.AddInt32(concurrent, 1)
		for {
			old := atomic.LoadInt32(maxConcurrent)
			if cur <= old {
				break
			}
			if atomic.CompareAndSwapInt32(maxConcurrent, old, cur) {
				break
			}
		}
		<-unblock
		atomic.AddInt32(concurrent, -1)
		if wg != nil {
			wg.Done()
		}
		return nil
	}
	return proc, unblock
}

func instantProcess(fn func()) queue.ProcessFunc {
	return func(_ queue.Item) error {
		if fn != nil {
			fn()
		}
		return nil
	}
}

// ---- Tests ----

func TestSingleGroupConcurrency(t *testing.T) {
	q := queue.New(4, 5*time.Second, 0)
	defer q.Shutdown()

	var concurrent, maxConcurrent int32
	var wg sync.WaitGroup

	procs := make([]queue.ProcessFunc, 3)
	unblocks := make([]chan struct{}, 3)
	for i := range 3 {
		wg.Add(1)
		procs[i], unblocks[i] = makeBlockingProc(&concurrent, &maxConcurrent, &wg)
	}

	for i := range 3 {
		q.Enqueue(queue.Item{GroupID: "g1"}, procs[i])
	}

	time.Sleep(50 * time.Millisecond)
	close(unblocks[0])
	time.Sleep(30 * time.Millisecond)
	close(unblocks[1])
	time.Sleep(30 * time.Millisecond)
	close(unblocks[2])
	wg.Wait()

	if atomic.LoadInt32(&maxConcurrent) > 1 {
		t.Errorf("max concurrent for single group = %d, want ≤1", maxConcurrent)
	}
}

func TestGlobalConcurrencyLimit(t *testing.T) {
	maxCont := 2
	q := queue.New(maxCont, 5*time.Second, 0)
	defer q.Shutdown()

	var concurrent, maxConcurrent int32
	var wg sync.WaitGroup

	procs := make([]queue.ProcessFunc, 3)
	unblocks := make([]chan struct{}, 3)
	for i := range 3 {
		wg.Add(1)
		procs[i], unblocks[i] = makeBlockingProc(&concurrent, &maxConcurrent, &wg)
	}

	for i := range 3 {
		q.Enqueue(queue.Item{GroupID: fmt.Sprintf("g%d", i)}, procs[i])
	}

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&maxConcurrent) > int32(maxCont) {
		t.Errorf("global concurrency exceeded: %d > %d", maxConcurrent, maxCont)
	}

	for _, u := range unblocks {
		close(u)
	}
	wg.Wait()
}

func TestTaskPriorityOverMessages(t *testing.T) {
	q := queue.New(1, 5*time.Second, 0)
	defer q.Shutdown()

	var order []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	record := func(label string) queue.ProcessFunc {
		return func(_ queue.Item) error {
			mu.Lock()
			order = append(order, label)
			mu.Unlock()
			wg.Done()
			return nil
		}
	}

	// Block queue with first message
	firstUnblock := make(chan struct{})
	firstProc := func(_ queue.Item) error {
		mu.Lock()
		order = append(order, "msg1")
		mu.Unlock()
		<-firstUnblock
		wg.Done()
		return nil
	}

	wg.Add(3)
	q.Enqueue(queue.Item{GroupID: "g1"}, firstProc)
	time.Sleep(20 * time.Millisecond)

	q.Enqueue(queue.Item{GroupID: "g1"}, record("msg2"))
	q.EnqueueTask(queue.Item{GroupID: "g1", TaskID: "t1", IsTask: true}, record("task1"))

	close(firstUnblock)
	wg.Wait()

	if len(order) < 3 {
		t.Fatalf("expected 3 entries, got %v", order)
	}
	if order[1] != "task1" {
		t.Errorf("expected task before msg2, got order: %v", order)
	}
}

func TestExponentialBackoffRetry(t *testing.T) {
	var calls int32
	proc := func(_ queue.Item) error {
		atomic.AddInt32(&calls, 1)
		return fmt.Errorf("fail")
	}

	q := queue.New(4, 30*time.Millisecond, 0)
	defer q.Shutdown()

	q.Enqueue(queue.Item{GroupID: "g1"}, proc)

	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt32(&calls) < 1 {
		t.Fatal("expected first call")
	}

	time.Sleep(60 * time.Millisecond) // first retry after ~30ms
	if atomic.LoadInt32(&calls) < 2 {
		t.Errorf("expected second call after backoff, got %d", calls)
	}
}

func TestShutdownPreventsNewEnqueues(t *testing.T) {
	var called int32
	proc := instantProcess(func() { atomic.AddInt32(&called, 1) })

	q := queue.New(4, 5*time.Second, 0)
	q.Shutdown()

	q.Enqueue(queue.Item{GroupID: "g1"}, proc)
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&called) != 0 {
		t.Error("processor should not be called after shutdown")
	}
}

func TestMaxRetriesExceeded(t *testing.T) {
	var calls int32
	proc := func(_ queue.Item) error {
		atomic.AddInt32(&calls, 1)
		return fmt.Errorf("fail")
	}

	q := queue.New(4, 5*time.Millisecond, 0)
	defer q.Shutdown()

	q.Enqueue(queue.Item{GroupID: "g1"}, proc)

	// Wait long enough for max retries (1 + 5 = 6 total, backoffs: 5,10,20,40,80ms)
	time.Sleep(400 * time.Millisecond)
	snapshot := atomic.LoadInt32(&calls)

	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&calls) != snapshot {
		t.Errorf("calls continued past max retries: %d → %d", snapshot, calls)
	}
	if snapshot > 6 {
		t.Errorf("expected ≤6 attempts, got %d", snapshot)
	}
}

func TestWaitingGroupsDrainWhenSlotFrees(t *testing.T) {
	q := queue.New(2, 5*time.Second, 0)
	defer q.Shutdown()

	var processed []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	procs := make([]queue.ProcessFunc, 3)
	unblocks := make([]chan struct{}, 3)
	for i := range 3 {
		wg.Add(1)
		i := i
		u := make(chan struct{})
		unblocks[i] = u
		procs[i] = func(_ queue.Item) error {
			<-u
			mu.Lock()
			processed = append(processed, fmt.Sprintf("g%d", i))
			mu.Unlock()
			wg.Done()
			return nil
		}
	}

	q.Enqueue(queue.Item{GroupID: "g0"}, procs[0])
	q.Enqueue(queue.Item{GroupID: "g1"}, procs[1])
	q.Enqueue(queue.Item{GroupID: "g2"}, procs[2])

	time.Sleep(50 * time.Millisecond)

	close(unblocks[0]) // free slot → g2 should start
	time.Sleep(50 * time.Millisecond)
	close(unblocks[1])
	close(unblocks[2])

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, p := range processed {
		if p == "g2" {
			found = true
		}
	}
	if !found {
		t.Error("g2 was never processed")
	}
}

func TestDuplicateTaskRejected(t *testing.T) {
	var dupCalls int32

	firstUnblock := make(chan struct{})
	firstProc := func(_ queue.Item) error {
		<-firstUnblock
		return nil
	}
	dupProc := func(_ queue.Item) error {
		atomic.AddInt32(&dupCalls, 1)
		return nil
	}

	q := queue.New(4, 5*time.Second, 0)
	defer q.Shutdown()

	q.EnqueueTask(queue.Item{GroupID: "g1", TaskID: "t1", IsTask: true}, firstProc)
	time.Sleep(20 * time.Millisecond)

	q.EnqueueTask(queue.Item{GroupID: "g1", TaskID: "t1", IsTask: true}, dupProc)
	close(firstUnblock)

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&dupCalls) != 0 {
		t.Error("duplicate task should not have run")
	}
}

func TestIdlePreemption(t *testing.T) {
	dir := t.TempDir()
	q := queue.New(4, 5*time.Second, 0)
	q.SetCloseDir(dir)
	defer q.Shutdown()

	containerDone := make(chan struct{})
	proc := func(_ queue.Item) error {
		<-containerDone
		return nil
	}

	q.Enqueue(queue.Item{GroupID: "g1"}, proc)
	time.Sleep(30 * time.Millisecond)

	q.EnqueueTask(queue.Item{GroupID: "g1", TaskID: "t1", IsTask: true}, instantProcess(nil))
	q.NotifyIdle("g1")

	time.Sleep(30 * time.Millisecond)

	if _, err := os.Stat(filepath.Join(dir, "g1", "_close")); err != nil {
		t.Errorf("expected _close file to be written on idle preemption: %v", err)
	}
	close(containerDone)
}

func TestNoIdlePreemptionWhenActive(t *testing.T) {
	dir := t.TempDir()
	q := queue.New(4, 5*time.Second, 0)
	q.SetCloseDir(dir)
	defer q.Shutdown()

	containerDone := make(chan struct{})
	proc := func(_ queue.Item) error {
		<-containerDone
		return nil
	}

	q.Enqueue(queue.Item{GroupID: "g1"}, proc)
	time.Sleep(30 * time.Millisecond)

	// Task queued but NotifyIdle NOT called — no preemption
	q.EnqueueTask(queue.Item{GroupID: "g1", TaskID: "t1", IsTask: true}, instantProcess(nil))
	time.Sleep(30 * time.Millisecond)

	if _, err := os.Stat(filepath.Join(dir, "g1", "_close")); err == nil {
		t.Error("_close should not be written when container is active (not idle)")
	}
	close(containerDone)
}

func TestMessageResetsIdle(t *testing.T) {
	dir := t.TempDir()
	q := queue.New(4, 5*time.Second, 0)
	q.SetCloseDir(dir)
	defer q.Shutdown()

	containerDone := make(chan struct{})
	proc := func(_ queue.Item) error {
		<-containerDone
		return nil
	}

	q.Enqueue(queue.Item{GroupID: "g1"}, proc)
	time.Sleep(30 * time.Millisecond)

	// SendMessage resets idle state
	q.SendMessage("g1", "hello")

	q.EnqueueTask(queue.Item{GroupID: "g1", TaskID: "t1", IsTask: true}, instantProcess(nil))
	q.NotifyIdle("g1")

	time.Sleep(30 * time.Millisecond)

	if _, err := os.Stat(filepath.Join(dir, "g1", "_close")); err == nil {
		t.Error("message should reset idle state, preventing preemption")
	}
	close(containerDone)
}

func TestSendMessageReturnsFalseForTaskContainer(t *testing.T) {
	q := queue.New(4, 5*time.Second, 0)
	defer q.Shutdown()

	containerDone := make(chan struct{})
	proc := func(_ queue.Item) error {
		<-containerDone
		return nil
	}

	q.EnqueueTask(queue.Item{GroupID: "g1", TaskID: "t1", IsTask: true}, proc)
	time.Sleep(30 * time.Millisecond)

	ok := q.SendMessage("g1", "hello")
	if ok {
		t.Error("SendMessage should return false for task container")
	}
	close(containerDone)
}
