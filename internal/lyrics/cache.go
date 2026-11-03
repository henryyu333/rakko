package lyrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// CacheTTL is how long one answer (including "no synced lyrics") is reused.
const CacheTTL = 7 * 24 * time.Hour

// cacheFileName is the small file inside the app's data directory.
const cacheFileName = "lyrics.json"

// cacheEntry is one track's answer as stored.
type cacheEntry struct {
	Synced       string    `json:"synced,omitempty"`
	Instrumental bool      `json:"instrumental,omitempty"`
	None         bool      `json:"none,omitempty"`
	At           time.Time `json:"at"`
}

// cacheFile is the on-disk envelope.
type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

// Cache keeps lyrics answers in memory and, when dir is set, in
// dir/lyrics.json. Reading is lazy; a missing or broken file is an empty
// cache, never an error to the page. Safe for concurrent use.
type Cache struct {
	dir   string
	mu    sync.Mutex
	load  bool
	items map[string]cacheEntry
	now   func() time.Time
}

// NewCache returns a cache over dir; "" keeps it in memory only.
func NewCache(dir string) *Cache {
	return &Cache{dir: dir, now: time.Now}
}

// Key identifies a track for the cache: case-folded title, artist and the
// rounded duration.
func Key(t Track) string {
	return normText(t.Title) + "|" + normText(t.Artist) + "|" + strconv.Itoa(t.Duration)
}

// Get answers a fresh entry, dropping an expired one.
func (c *Cache) Get(key string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	e, ok := c.items[key]
	if !ok {
		return Result{}, false
	}
	if c.now().Sub(e.At) > CacheTTL {
		delete(c.items, key)
		return Result{}, false
	}
	return Result{Synced: e.Synced, Instrumental: e.Instrumental, None: e.None}, true
}

// Put stores an answer and writes the file. A write failure is dropped: the
// in-memory answer still serves this run.
func (c *Cache) Put(key string, r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	c.items[key] = cacheEntry{Synced: r.Synced, Instrumental: r.Instrumental, None: r.None, At: c.now()}
	// Expire while writing, so a long-lived cache file stays small.
	for k, e := range c.items {
		if c.now().Sub(e.At) > CacheTTL {
			delete(c.items, k)
		}
	}
	c.writeLocked()
}

func (c *Cache) loadLocked() {
	if c.load {
		return
	}
	c.load = true
	c.items = map[string]cacheEntry{}
	if c.dir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(c.dir, cacheFileName))
	if err != nil {
		return
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	for k, e := range f.Entries {
		c.items[k] = e
	}
}

func (c *Cache) writeLocked() {
	if c.dir == "" {
		return
	}
	data, err := json.Marshal(cacheFile{Version: 1, Entries: c.items})
	if err != nil {
		return
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(c.dir, "."+cacheFileName+".tmp-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(name, filepath.Join(c.dir, cacheFileName))
}
