package platform

// A rebuildable holder for one apartment-threaded COM interface pointer
// whose server can go away under it: Explorer's restart disconnects the
// shell's IVirtualDesktopManager, and a handle cached for the process's
// lifetime stops following the user's desktops until the app restarts.
// The holder re-creates the object when a call answers that its server
// died, and asks the server again on a doubling backoff while it is down,
// so a dead Explorer is neither hammered on every poll nor mourned in the
// log a hundred times. The holder is driven from the main thread only.

import (
	"errors"
	"time"
)

const (
	// comBackoffStep is the wait after the first failed create, and the
	// doubling base of the next ones.
	comBackoffStep = 500 * time.Millisecond
	// comBackoffMax caps the backoff while the server stays down.
	comBackoffMax = 30 * time.Second
)

// comObjectCache holds one COM interface pointer, created on first use
// and recreated after its server goes away.
type comObjectCache struct {
	// create makes the object; release lets a dead one go; isDead says a
	// call's HRESULT means the object's server is gone. All main-thread.
	create  func() (uintptr, error)
	release func(uintptr)
	isDead  func(hresult uintptr) bool
	// explain, when set, is told once when an outage starts and once when
	// it ends, so the log carries one line per outage instead of one per
	// refused poll.
	explain func(down bool, err error)

	obj     uintptr
	err     error     // why the last create failed; nil while an object lives
	nextTry time.Time // before this a create is refused
	fails   int       // consecutive failed creates, the backoff's exponent
	down    bool      // the outage is on and was explained
}

// get returns the live object, creating it on first use — once the
// backoff of the last failed try has passed. 0 with the outage's reason
// while the backoff holds the caller off.
func (c *comObjectCache) get(now time.Time) (uintptr, error) {
	if c.obj != 0 {
		return c.obj, nil
	}
	if now.Before(c.nextTry) {
		return 0, c.err
	}
	obj, err := c.create()
	if err != nil {
		c.throttle(now, err)
		if !c.down {
			c.down = true
			if c.explain != nil {
				c.explain(true, err)
			}
		}
		return 0, err
	}
	c.obj, c.err, c.fails = obj, nil, 0
	c.nextTry = time.Time{}
	if c.down {
		c.down = false
		if c.explain != nil {
			c.explain(false, nil)
		}
	}
	return obj, nil
}

// throttle arms the backoff after another consecutive failure, and keeps
// the reason a later refusal reports.
func (c *comObjectCache) throttle(now time.Time, err error) {
	c.fails++
	c.err = err
	c.nextTry = now.Add(min(comBackoffStep<<uint(min(c.fails-1, 6)), comBackoffMax))
}

// dead lets go of the object whose call answered that its server went
// away: the next get creates a fresh one, right away — the object was
// healthy a moment ago, so the backoff only applies once a create fails.
func (c *comObjectCache) dead(hresult uintptr) {
	if c.obj == 0 || !c.isDead(hresult) {
		return
	}
	c.release(c.obj)
	c.obj = 0
}

// call runs fn against the live object, rebuilding it once when fn's
// HRESULT says its server went away. ok is false when no object could be
// had at all; hr is 0 then.
func (c *comObjectCache) call(now time.Time, fn func(obj uintptr) uintptr) (uintptr, bool) {
	obj, _ := c.get(now)
	if obj == 0 {
		return 0, false
	}
	hr := fn(obj)
	if !c.isDead(hr) {
		return hr, true
	}
	c.dead(hr)
	if obj, _ = c.get(now); obj == 0 {
		return 0, false
	}
	hr = fn(obj)
	if c.isDead(hr) {
		// A fresh object answered dead too: let go of it and arm the
		// backoff, so a server that stays down is re-asked on the
		// schedule of a failed create, not on every poll tick.
		c.dead(hr)
		c.throttle(now, errServerGone)
		if !c.down {
			c.down = true
			if c.explain != nil {
				c.explain(true, errServerGone)
			}
		}
	}
	return hr, true
}

// errServerGone is the refusal a still-dead object leaves behind.
var errServerGone = errors.New("the object's server went away")
