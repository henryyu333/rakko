package platform

// The rebuildable COM holder (com_cache.go) against a scripted create,
// release and call: an object whose server went away is released and
// recreated on the next ask; a server that stays down is re-asked on a
// doubling backoff instead of every poll tick; the outage is explained
// once and its end once; and recovery puts following back on the live
// object.

import (
	"errors"
	"testing"
	"time"
)

// fakeComServer stands in for Explorer: it hands out numbered objects and
// counts every release, and can be told to refuse creation.
type fakeComServer struct {
	next     uintptr
	refuse   error
	asks     int // every create the holder made, refused ones included
	released []uintptr
}

func (s *fakeComServer) create() (uintptr, error) {
	s.asks++
	if s.refuse != nil {
		return 0, s.refuse
	}
	s.next++
	return s.next, nil
}

func (s *fakeComServer) release(obj uintptr) { s.released = append(s.released, obj) }

// deadAnswer is an HRESULT of a server that went away, as the Windows
// build judges them (pin_windows.go's isCOMDisconnected).
const deadAnswer = uintptr(0x80010108)

func isDeadAnswer(hr uintptr) bool { return hr == deadAnswer }

func newTestCache(s *fakeComServer, explain func(down bool, err error)) *comObjectCache {
	return &comObjectCache{
		create:  s.create,
		release: s.release,
		isDead:  isDeadAnswer,
		explain: explain,
	}
}

func TestComObjectCacheCreatesOnceAndReuses(t *testing.T) {
	s := &fakeComServer{}
	c := newTestCache(s, nil)
	now := time.Now()
	obj, err := c.get(now)
	if err != nil || obj != 1 {
		t.Fatalf("first get = %d, %v; want 1, nil", obj, err)
	}
	again, _ := c.get(now.Add(time.Second))
	if again != 1 || s.asks != 1 {
		t.Fatalf("second get = %d after %d asks; want the cached object", again, s.asks)
	}
}

// A call that answers the server went away releases the dead object and
// retries once on a fresh one, without waiting out any backoff.
func TestComObjectCacheRebuildsAfterADeadCall(t *testing.T) {
	s := &fakeComServer{}
	c := newTestCache(s, nil)
	now := time.Now()
	answers := []uintptr{deadAnswer, 0}
	calls := 0
	hr, ok := c.call(now, func(obj uintptr) uintptr {
		calls++
		if obj != 1 && obj != 2 {
			t.Errorf("call ran against object %d", obj)
		}
		return answers[min(calls-1, len(answers)-1)]
	})
	if !ok || hr != 0 {
		t.Fatalf("call = %#x, %v; want the retried answer, true", hr, ok)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want the dead one and the retried one", calls)
	}
	if len(s.released) != 1 || s.released[0] != 1 {
		t.Fatalf("released %v, want only the dead object", s.released)
	}
	if obj, _ := c.get(now); obj != 2 {
		t.Fatalf("the holder kept object %d, want the rebuilt 2", obj)
	}
}

// A call that stays dead on the fresh object releases it too; the next
// ask then waits for the backoff rather than hammering the server.
func TestComObjectCacheStayingDeadWaitsForTheBackoff(t *testing.T) {
	s := &fakeComServer{}
	c := newTestCache(s, nil)
	now := time.Now()
	calls := 0
	if _, ok := c.call(now, func(uintptr) uintptr {
		calls++
		return deadAnswer
	}); !ok || calls != 2 {
		t.Fatalf("call = %d calls, ok as reported; want two dead tries", calls)
	}
	if len(s.released) != 2 {
		t.Fatalf("a still-dead object was kept: released %v", s.released)
	}
	if obj, _ := c.get(now.Add(time.Millisecond)); obj != 0 || s.asks != 2 {
		t.Fatalf("an ask inside the backoff created again: object %d, asks %d", obj, s.asks)
	}
}

// A create that keeps failing is re-asked on a doubling backoff, and the
// outage is explained once, its end once.
func TestComObjectCacheBacksOffWhileTheCreateFails(t *testing.T) {
	s := &fakeComServer{refuse: errors.New("explorer is down")}
	var events []string
	c := newTestCache(s, func(down bool, err error) {
		if down {
			events = append(events, "down")
			return
		}
		events = append(events, "up")
	})
	now := time.Now()
	if obj, err := c.get(now); obj != 0 || err == nil {
		t.Fatalf("first get = %d, %v; want the refusal", obj, err)
	}
	if s.asks != 1 {
		t.Fatalf("the first get asked %d times", s.asks)
	}
	if len(events) != 1 || events[0] != "down" {
		t.Fatalf("outage events = %v, want one down", events)
	}
	// Inside the backoff: refused without asking the server again.
	if _, _ = c.get(now.Add(comBackoffStep - time.Millisecond)); s.asks != 1 {
		t.Fatalf("the backoff re-asked the server %d times", s.asks)
	}
	// One step out: the backoff fires and is refused again, doubling the
	// next wait.
	if _, _ = c.get(now.Add(comBackoffStep)); s.asks != 2 {
		t.Fatalf("re-asks after one step = %d, want 2", s.asks)
	}
	// The doubled wait holds a second step off.
	if _, _ = c.get(now.Add(2 * comBackoffStep)); s.asks != 2 {
		t.Fatalf("the doubled backoff re-asked early: %d asks", s.asks)
	}
	// The recovery: one good create clears the backoff and says so once.
	s.refuse = nil
	if obj, err := c.get(now.Add(4 * comBackoffStep)); err != nil || obj == 0 {
		t.Fatalf("recovered get = %d, %v", obj, err)
	}
	if obj, _ := c.get(now.Add(5 * comBackoffStep)); obj == 0 {
		t.Fatal("the holder did not keep the recovered object")
	}
	if len(events) != 2 || events[1] != "up" {
		t.Fatalf("recovery events = %v, want one up after the down", events)
	}
}

// A dead report that is not the HRESULT of a gone server releases
// nothing.
func TestComObjectCacheIgnoresNonDeadReports(t *testing.T) {
	s := &fakeComServer{}
	c := newTestCache(s, nil)
	now := time.Now()
	c.dead(uintptr(0x80004005)) // E_FAIL, an ordinary failure
	if obj, _ := c.get(now); obj != 1 || len(s.released) != 0 {
		t.Fatalf("an ordinary failure released the object: %v", s.released)
	}
}

// With no object to call through, call skips the caller entirely.
func TestComObjectCacheCallSkipsWithoutAnObject(t *testing.T) {
	s := &fakeComServer{refuse: errors.New("explorer is down")}
	c := newTestCache(s, nil)
	ran := false
	if _, ok := c.call(time.Now(), func(uintptr) uintptr {
		ran = true
		return 0
	}); ok || ran {
		t.Fatal("call ran without a live object")
	}
}
