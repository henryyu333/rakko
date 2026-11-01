//go:build darwin

package mac

// Spotlight search over the system's own index (CoreServices MDQuery).
//
// The query runs on its own goroutine and on a private dispatch queue, so
// neither the main thread nor the caller waits for it; results stream out
// in batches as the index finds them, and Cancel stops a query whose text
// the user has already replaced. Nothing here builds a second index: every
// hit is a row the system already knows.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
)

// SpotlightHit is one result of a Spotlight query.
type SpotlightHit struct {
	Path     string
	Name     string
	App      bool
	Folder   bool
	LastUsed time.Time
}

// SpotlightQuery builds the MDQuery expression for term: the file name
// contains it (case- and diacritic-insensitive), or the document's text
// does. Quotes, backslashes and wildcards are escaped so the term is
// literal. Spotlight's text matcher does not honour those escapes — an
// escaped wildcard there matches everything — so a term carrying one
// searches names only. An empty term has no query.
func SpotlightQuery(term string) string {
	term = strings.TrimSpace(term)
	if term == "" {
		return ""
	}
	e := spotlightEscape(term)
	name := `(kMDItemFSName == "*` + e + `*"cd) || (kMDItemDisplayName == "*` + e + `*"cd)`
	if strings.ContainsAny(term, `"\\*?`) {
		return name
	}
	return name + ` || (kMDItemTextContent == "*` + e + `*"cd)`
}

// spotlightEscape makes term a literal inside an MDQuery string: a
// backslash escapes the quote, the backslash itself and both wildcards.
func spotlightEscape(term string) string {
	var b strings.Builder
	b.Grow(len(term) + 4)
	for _, r := range term {
		switch r {
		case '\\', '"', '*', '?':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SpotlightPathWanted reports whether a hit belongs in the results: not a
// hidden file, not inside ~/Library, a cache, a package (.app, .bundle,
// .framework) or the per-user temporary tree. Paths outside the home are
// kept, including /Applications and /System/Applications.
func SpotlightPathWanted(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") {
		return false
	}
	if home := spotHome(); home != "" && strings.HasPrefix(path, home+"/Library/") {
		return false
	}
	if strings.Contains(path, "/var/folders/") {
		return false
	}
	for _, pkg := range []string{".app/", ".bundle/", ".framework/", ".photoslibrary/"} {
		if strings.Contains(path, pkg) {
			return false
		}
	}
	for _, part := range strings.Split(path[1:], "/") {
		if strings.HasPrefix(part, ".") || strings.EqualFold(part, "Caches") {
			return false
		}
	}
	return true
}

var spotHomeDir = sync.OnceValue(func() string {
	home, _ := os.UserHomeDir()
	return home
})

func spotHome() string { return spotHomeDir() }

// WarmSpotlight primes MDQuery's connection to the index, so the first
// search of a session answers as fast as the ones after it. The query
// matches nothing and its results are discarded; it is safe to call more
// than once.
func WarmSpotlight() {
	go func() {
		done := make(chan struct{})
		StartSpotlight("zzz-rakko-warmup-zzz", 1, func([]SpotlightHit) {}, func() { close(done) })
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}()
}

// SpotlightRun is one query in flight. Cancel stops it; the batches it
// would still deliver are dropped.
type SpotlightRun struct {
	mu      sync.Mutex
	q       uintptr
	stopped atomic.Bool
}

// Cancel stops the query. Safe from any goroutine and more than once.
func (r *SpotlightRun) Cancel() {
	if r == nil {
		return
	}
	r.stopped.Store(true)
	r.mu.Lock()
	if r.q != 0 {
		spotQueryStop(r.q)
	}
	r.mu.Unlock()
}

// StartSpotlight runs term against the Spotlight index. emit receives each
// batch of new hits on the query's own goroutine, in arrival order; done
// runs once the index has finished gathering (or immediately for an empty
// term). limit caps how many hits the query collects. The returned run is
// never nil, so a caller can always cancel it.
func StartSpotlight(term string, limit int, emit func([]SpotlightHit), done func()) *SpotlightRun {
	run := &SpotlightRun{}
	if strings.TrimSpace(term) == "" {
		if done != nil {
			done()
		}
		return run
	}
	go run.loop(term, limit, emit, done)
	return run
}

func (r *SpotlightRun) loop(term string, limit int, emit func([]SpotlightHit), done func()) {
	// MDQuery is a CoreFoundation object with a run-loop ancestry: keep the
	// goroutine on one thread for the query's whole life, even though the
	// dispatch queue, not a run loop, delivers its results.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		r.mu.Lock()
		if r.q != 0 {
			spotRelease(r.q)
			r.q = 0
		}
		r.mu.Unlock()
		if done != nil && !r.stopped.Load() {
			done()
		}
	}()
	if err := spotLoad(); err != nil {
		return
	}
	expr := SpotlightQuery(term)
	if expr == "" {
		return
	}
	if limit <= 0 {
		limit = spotLimit
	}
	q := spotQueryCreate(0, spotString(expr), 0, 0)
	if q == 0 {
		return
	}
	spotQuerySetMax(q, int64(limit))
	spotQuerySetQueue(q, spotQueue())
	r.mu.Lock()
	r.q = q
	r.mu.Unlock()
	if !spotQueryExecute(q, 0) {
		return
	}
	seen := int64(0)
	var pending []SpotlightHit
	last := time.Now()
	first := true
	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		if r.stopped.Load() {
			pending = nil
			return false
		}
		emit(pending)
		pending = nil
		last = time.Now()
		first = false
		return true
	}
	for {
		if r.stopped.Load() {
			return
		}
		r.mu.Lock()
		if r.q == 0 {
			r.mu.Unlock()
			return
		}
		n := spotQueryCount(q)
		complete := spotQueryComplete(q)
		for seen < n && len(pending) < spotBatchMax {
			if first && len(pending) > 0 {
				// Paint the first row as soon as it exists; the rest of
				// the batch follows on the next pass.
				break
			}
			item := spotQueryResult(q, seen)
			seen++
			if item == 0 {
				continue
			}
			if hit, ok := spotHit(item); ok {
				pending = append(pending, hit)
			}
		}
		r.mu.Unlock()
		if len(pending) > 0 && (first || len(pending) >= spotBatchMax || time.Since(last) >= spotBatchEvery) {
			if !flush() {
				return
			}
		}
		if complete && seen >= n {
			break
		}
		if seen >= n || len(pending) == 0 {
			time.Sleep(spotPoll)
		}
	}
	// The index is done: read whatever arrived since the last pass.
	r.mu.Lock()
	if r.q != 0 {
		for n := spotQueryCount(q); seen < n; seen++ {
			if item := spotQueryResult(q, seen); item != 0 {
				if hit, ok := spotHit(item); ok {
					pending = append(pending, hit)
				}
			}
		}
	}
	r.mu.Unlock()
	flush()
}

const (
	spotLimit      = 400 // hits collected per query; ranked down to a few rows
	spotBatchMax   = 24  // hits per emit while the index is still gathering
	spotBatchEvery = 40 * time.Millisecond
	spotPoll       = 5 * time.Millisecond
)

// spotHit reads one MDItem into a hit, or reports false when the item has
// no usable path or lives where the results never show.
func spotHit(item uintptr) (SpotlightHit, bool) {
	path := spotAttrString(item, spotAttrPath())
	if !SpotlightPathWanted(path) {
		return SpotlightHit{}, false
	}
	hit := SpotlightHit{Path: path, Name: spotAttrString(item, spotAttrName())}
	if hit.Name == "" {
		hit.Name = filepath.Base(path)
	}
	for _, kind := range spotAttrStrings(item, spotAttrTree()) {
		switch kind {
		case "com.apple.application-bundle":
			hit.App = true
		case "public.folder":
			hit.Folder = true
		}
	}
	if hit.App {
		hit.Folder = false
	}
	hit.LastUsed = spotAttrDate(item, spotAttrUsed())
	return hit, true
}

var (
	spotLoadOnce sync.Once
	spotLoadErr  error

	spotStringCreate func(alloc uintptr, cstr *byte, enc uint32) uintptr
	spotStringLength func(s uintptr) int64
	spotStringCStr   func(s uintptr, buf *byte, size int64, enc uint32) bool
	spotArrayCount   func(a uintptr) int64
	spotArrayValue   func(a uintptr, i int64) uintptr
	spotDateAbs      func(d uintptr) float64
	spotRelease      func(v uintptr)

	spotQueryCreate   func(alloc, query, values, sorting uintptr) uintptr
	spotQuerySetQueue func(q, queue uintptr)
	spotQuerySetMax   func(q uintptr, n int64)
	spotQueryExecute  func(q uintptr, flags uint64) bool
	spotQueryComplete func(q uintptr) bool
	spotQueryCount    func(q uintptr) int64
	spotQueryResult   func(q uintptr, i int64) uintptr
	spotQueryStop     func(q uintptr)
	spotItemAttr      func(item, attr uintptr) uintptr
	spotQueueCreate   func(label *byte, attr uintptr) uintptr
	spotQueueOnce     sync.Once
	spotQueueRef      uintptr
	spotAttrPathCF    uintptr
	spotAttrNameCF    uintptr
	spotAttrTreeCF    uintptr
	spotAttrUsedCF    uintptr
	spotAttrOnce      sync.Once
)

const spotUTF8 = 0x08000100 // kCFStringEncodingUTF8

// spotLoad resolves CoreFoundation, Metadata and libdispatch once.
func spotLoad() error {
	spotLoadOnce.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			spotLoadErr = err
			return
		}
		md, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/Frameworks/Metadata.framework/Metadata",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			spotLoadErr = err
			return
		}
		disp, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			spotLoadErr = err
			return
		}
		purego.RegisterLibFunc(&spotStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&spotStringLength, cf, "CFStringGetLength")
		purego.RegisterLibFunc(&spotStringCStr, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&spotArrayCount, cf, "CFArrayGetCount")
		purego.RegisterLibFunc(&spotArrayValue, cf, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&spotDateAbs, cf, "CFDateGetAbsoluteTime")
		purego.RegisterLibFunc(&spotRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&spotQueryCreate, md, "MDQueryCreate")
		purego.RegisterLibFunc(&spotQuerySetQueue, md, "MDQuerySetDispatchQueue")
		purego.RegisterLibFunc(&spotQuerySetMax, md, "MDQuerySetMaxCount")
		purego.RegisterLibFunc(&spotQueryExecute, md, "MDQueryExecute")
		purego.RegisterLibFunc(&spotQueryComplete, md, "MDQueryIsGatheringComplete")
		purego.RegisterLibFunc(&spotQueryCount, md, "MDQueryGetResultCount")
		purego.RegisterLibFunc(&spotQueryResult, md, "MDQueryGetResultAtIndex")
		purego.RegisterLibFunc(&spotQueryStop, md, "MDQueryStop")
		purego.RegisterLibFunc(&spotItemAttr, md, "MDItemCopyAttribute")
		purego.RegisterLibFunc(&spotQueueCreate, disp, "dispatch_queue_create")
	})
	return spotLoadErr
}

// spotQueue is the private serial queue MDQuery delivers on; one for the
// process, created on first use.
func spotQueue() uintptr {
	spotQueueOnce.Do(func() {
		label := append([]byte("local.y3.rakko.spotlight"), 0)
		spotQueueRef = spotQueueCreate(&label[0], 0)
	})
	return spotQueueRef
}

func spotAttrPath() uintptr { spotAttrs(); return spotAttrPathCF }
func spotAttrName() uintptr { spotAttrs(); return spotAttrNameCF }
func spotAttrTree() uintptr { spotAttrs(); return spotAttrTreeCF }
func spotAttrUsed() uintptr { spotAttrs(); return spotAttrUsedCF }

func spotAttrs() {
	spotAttrOnce.Do(func() {
		spotAttrPathCF = spotString("kMDItemPath")
		spotAttrNameCF = spotString("kMDItemDisplayName")
		spotAttrTreeCF = spotString("kMDItemContentTypeTree")
		spotAttrUsedCF = spotString("kMDItemLastUsedDate")
	})
}

func spotString(s string) uintptr {
	b := append([]byte(s), 0)
	return spotStringCreate(0, &b[0], spotUTF8)
}

func spotGoString(cf uintptr) string {
	if cf == 0 {
		return ""
	}
	n := spotStringLength(cf)
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n*4+1)
	if !spotStringCStr(cf, &buf[0], int64(len(buf)), spotUTF8) {
		return ""
	}
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// spotAttrString copies one string attribute of item and releases it.
func spotAttrString(item, attr uintptr) string {
	v := spotItemAttr(item, attr)
	if v == 0 {
		return ""
	}
	defer spotRelease(v)
	return spotGoString(v)
}

// spotAttrStrings copies a string-array attribute of item and releases it.
func spotAttrStrings(item, attr uintptr) []string {
	v := spotItemAttr(item, attr)
	if v == 0 {
		return nil
	}
	defer spotRelease(v)
	n := spotArrayCount(v)
	if n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := int64(0); i < n; i++ {
		if s := spotGoString(spotArrayValue(v, i)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// spotAttrDate copies a date attribute of item and releases it. CFDate's
// absolute time counts seconds from 2001-01-01 UTC.
func spotAttrDate(item, attr uintptr) time.Time {
	v := spotItemAttr(item, attr)
	if v == 0 {
		return time.Time{}
	}
	defer spotRelease(v)
	secs := spotDateAbs(v)
	if secs == 0 {
		return time.Time{}
	}
	return time.Unix(978307200+int64(secs), 0)
}
