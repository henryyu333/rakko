//go:build darwin

package mac

import (
	"reflect"
	"sync"
	"testing"
)

// TestWakeQueueKeepsEveryJob: two wakeups queued before the main loop runs
// must both run, in order, instead of the second overwriting the first.
func TestWakeQueueKeepsEveryJob(t *testing.T) {
	var q mainWakeQueue
	var mu sync.Mutex
	var seen []int
	q.push(func() { mu.Lock(); seen = append(seen, 1); mu.Unlock() })
	q.push(func() { mu.Lock(); seen = append(seen, 2); mu.Unlock() })
	for {
		jobs := q.drain()
		if len(jobs) == 0 {
			break
		}
		for _, fn := range jobs {
			fn()
		}
	}
	if !reflect.DeepEqual(seen, []int{1, 2}) {
		t.Fatalf("queued wakeups ran as %v, want [1 2]", seen)
	}
	if !q.empty() {
		t.Fatal("queue not empty after drain")
	}
}

// TestWakeQueueDrainsJobsEnqueuedWhileRunning: a callback may enqueue more
// work; the drain loop must pick it up before returning.
func TestWakeQueueDrainsJobsEnqueuedWhileRunning(t *testing.T) {
	var q mainWakeQueue
	seen := 0
	q.push(func() {
		seen++
		q.push(func() { seen++ })
	})
	for {
		jobs := q.drain()
		if len(jobs) == 0 {
			break
		}
		for _, fn := range jobs {
			fn()
		}
	}
	if seen != 2 {
		t.Fatalf("ran %d jobs, want 2", seen)
	}
}
