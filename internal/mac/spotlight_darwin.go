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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// SpotlightHit is one result of a Spotlight query.
type SpotlightHit struct {
	Path     string
	Name     string
	App      bool
	Folder   bool
	LastUsed time.Time
}

// SpotlightQueries builds the MDQuery expressions for term: one asks the
// name index (case- and diacritic-insensitive), the other the text
// content index. They run side by side because the text index answers
// much later — a name hit paints the sheet while the content hits are
// still being gathered. Quotes, backslashes and wildcards are escaped so
// the term is literal; Spotlight's text matcher does not honour those
// escapes (an escaped wildcard there matches everything), so a term
// carrying one searches names only. An empty term has no query.
func SpotlightQueries(term string) []string {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil
	}
	e := spotlightEscape(term)
	name := `(kMDItemFSName == "*` + e + `*"cd) || (kMDItemDisplayName == "*` + e + `*"cd)`
	if strings.ContainsAny(term, `"\\*?`) {
		return []string{name}
	}
	return []string{name, `(kMDItemTextContent == "*` + e + `*"cd)`}
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
// than once. A warmup that outlives its budget is cancelled: the index can
// be slow to start, and the user's first real query must not queue behind
// it.
func WarmSpotlight() {
	go func() {
		done := make(chan struct{})
		run := spotWarmStart("zzz-rakko-warmup-zzz", 1, func([]SpotlightHit) {}, func() { close(done) })
		select {
		case <-done:
		case <-time.After(spotWarmTimeout):
			run.Cancel()
		}
	}()
}

// spotWarmStart and spotWarmTimeout are seams for the warmup's test.
var (
	spotWarmStart   = StartSpotlight
	spotWarmTimeout = 2 * time.Second
)

// SpotlightRun is the queries one term runs. Cancel stops them; the
// batches they would still deliver are dropped.
type SpotlightRun struct {
	mu      sync.Mutex
	qs      []uintptr
	emitMu  sync.Mutex
	stopped atomic.Bool
}

// Cancel stops every query of the run. Safe from any goroutine and more
// than once.
func (r *SpotlightRun) Cancel() {
	if r == nil {
		return
	}
	r.stopped.Store(true)
	r.mu.Lock()
	for _, q := range r.qs {
		spotQueryStop(q)
	}
	r.mu.Unlock()
}

// StartSpotlight runs term against the Spotlight index. The name and the
// text queries run side by side; emit receives each batch of new hits on
// the query goroutines (never two at once), and done runs once both have
// finished gathering (or immediately for an empty term). limit caps how
// many hits each query collects. The returned run is never nil, so a
// caller can always cancel it.
func StartSpotlight(term string, limit int, emit func([]SpotlightHit), done func()) *SpotlightRun {
	run := &SpotlightRun{}
	exprs := SpotlightQueries(term)
	if len(exprs) == 0 {
		if done != nil {
			done()
		}
		return run
	}
	if limit <= 0 {
		limit = spotLimit
	}
	var wg sync.WaitGroup
	wg.Add(len(exprs))
	for _, expr := range exprs {
		expr := expr
		go func() {
			defer wg.Done()
			run.query(expr, limit, emit)
		}()
	}
	go func() {
		wg.Wait()
		if done != nil && !run.stopped.Load() {
			done()
		}
	}()
	return run
}

// query runs one expression to completion, emitting its batches as they
// arrive. The run's emit lock keeps the two queries' batches apart.
func (r *SpotlightRun) query(expr string, limit int, emit func([]SpotlightHit)) {
	// MDQuery is a CoreFoundation object with a run-loop ancestry: keep the
	// goroutine on one thread for the query's whole life, even though the
	// dispatch queue, not a run loop, delivers its results.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := spotLoad(); err != nil {
		return
	}
	query := spotString(expr)
	q := spotQueryCreate(0, query, 0, 0)
	// MDQueryCreate copies (retains) the query string: ours is done with.
	spotRelease(query)
	if q == 0 {
		return
	}
	spotQuerySetMax(q, int64(limit))
	spotQuerySetQueue(q, spotQueue())
	r.mu.Lock()
	if r.stopped.Load() {
		r.mu.Unlock()
		spotRelease(q)
		return
	}
	r.qs = append(r.qs, q)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.qs = slices.DeleteFunc(r.qs, func(v uintptr) bool { return v == q })
		r.mu.Unlock()
		// Stop first: no further result update may be queued. Then wait
		// for the queue to run out the ones already handed to it, so the
		// release below cannot pull the query out from under a callback.
		spotQueryStop(q)
		spotDrainQueue()
		spotRelease(q)
	}()
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
		r.emitMu.Lock()
		emit(pending)
		r.emitMu.Unlock()
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
		// The query runs on the dispatch queue: freeze its result set while
		// this goroutine reads it, then let it continue.
		spotQueryDisable(q)
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
		spotQueryEnable(q)
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
	spotQueryDisable(q)
	for n := spotQueryCount(q); seen < n; seen++ {
		if item := spotQueryResult(q, seen); item != 0 {
			if hit, ok := spotHit(item); ok {
				pending = append(pending, hit)
			}
		}
	}
	spotQueryEnable(q)
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
			hit.App = spotInstalledApp(path)
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

// spotInstalledApp reports whether a bundle lives where applications are
// installed: /Applications, /System/Applications or the user's own
// Applications folder. A .app anywhere else — a build folder, Downloads, a
// mounted image — lists as a file, not as an app.
func spotInstalledApp(path string) bool {
	roots := []string{"/Applications/", "/System/Applications/"}
	if home := spotHome(); home != "" {
		roots = append(roots, home+"/Applications/")
	}
	for _, root := range roots {
		if strings.HasPrefix(path, root) {
			return true
		}
	}
	return false
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
	spotQueryDisable  func(q uintptr)
	spotQueryEnable   func(q uintptr)
	spotSync          func(queue, block uintptr)
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
		purego.RegisterLibFunc(&spotQueryDisable, md, "MDQueryDisableUpdates")
		purego.RegisterLibFunc(&spotQueryEnable, md, "MDQueryEnableUpdates")
		purego.RegisterLibFunc(&spotSync, disp, "dispatch_sync")
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

// spotDrainQueue waits until everything queued on the query's dispatch
// queue has run: after MDQueryStop no new work is queued, so the wait bounds
// the query's callbacks that may still hold it.
func spotDrainQueue() {
	block := objc.NewBlock(func(objc.Block) {})
	defer block.Release()
	spotSync(spotQueue(), uintptr(block))
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
