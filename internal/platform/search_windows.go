//go:build windows

package platform

// Search over the Windows index. Files come from the system's own
// content index through the Windows Search OLE DB provider, streamed in
// batches while the user types; applications come from the shell's Apps
// folder, which is what the Start Menu and `shell:AppsFolder`
// enumerate. When the index cannot answer (the Windows Search service is
// stopped or its index disabled), the common folders are scanned directly
// instead, which finds names but not file contents — the same limit the
// Mac fallback has.

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SearchScope is what a search looks at.
type SearchScope int

const (
	ScopeAll   SearchScope = iota // names and text content, everything
	ScopeApps                     // application entries, by name only
	ScopeFiles                    // names and text content, bundles left out
)

// wants reports whether a hit belongs to the scope's results.
func (s SearchScope) wants(h SearchHit) bool {
	switch s {
	case ScopeApps:
		return h.App
	case ScopeFiles:
		return !h.App
	}
	return true
}

// SearchHit is one result.
type SearchHit struct {
	Path     string
	Name     string
	App      bool
	Folder   bool
	LastUsed time.Time
}

// SearchRun is one term's queries.
type SearchRun struct {
	canceled atomic.Bool
}

// Cancel stops the run; batches that would still arrive are dropped.
func (r *SearchRun) Cancel() {
	if r != nil {
		r.canceled.Store(true)
	}
}

const (
	searchLimit      = 400 // hits collected per query
	searchBatchMax   = 24  // hits per emit while gathering
	searchBatchEvery = 40 * time.Millisecond
	searchAppMaxAge  = time.Minute
)

// StartSearch runs term, in scope. The application list answers at once;
// the index query streams its batches on its own goroutine. Only the
// scope's own kind of hit is emitted. The returned run is never nil.
func StartSearch(term string, scope SearchScope, limit int, emit func([]SearchHit), done func()) *SearchRun {
	run := &SearchRun{}
	term = strings.TrimSpace(term)
	if term == "" {
		if done != nil {
			done()
		}
		return run
	}
	if limit <= 0 {
		limit = searchLimit
	}
	go func() {
		// A COM apartment of its own: the OLE DB provider and the shell
		// enumeration both need one, and the main thread's belongs to the UI.
		// The thread returns to Go's pool, so it must leave the apartment
		// it joined: a thread left in an STA makes the media worker's MTA
		// request fail when it is reused.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		leave, _ := enterCOMApartment(coinitApartmentThreaded)
		defer leave()
		w := newSearchSink(run, emit, limit)
		if scope != ScopeFiles {
			lower := strings.ToLower(term)
			for _, app := range appListFresh() {
				if w.stopped() {
					return
				}
				if strings.Contains(strings.ToLower(app.Name), lower) {
					w.add(SearchHit{Path: app.Target, Name: app.Name, App: true})
				}
			}
		}
		if !w.stopped() && scope != ScopeApps {
			if err := searchIndex(term, scope, w); err != nil {
				searchScan(term, scope, w)
			}
		}
		w.flush(true)
		if !w.stopped() && done != nil {
			done()
		}
	}()
	return run
}

// WarmSearch pays the first query's setup: the shell's Apps folder is
// enumerated and the index is asked a question that matches nothing, so
// the service and provider start before the user types.
func WarmSearch() {
	go func() {
		appListFresh()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		leave, _ := enterCOMApartment(coinitApartmentThreaded)
		defer leave()
		_ = searchIndex("zzz-rakko-warmup-zzz", ScopeAll, newSearchSink(&SearchRun{}, func([]SearchHit) {}, 1))
	}()
}

// searchSink batches hits for one run.
type searchSink struct {
	run   *SearchRun
	emit  func([]SearchHit)
	limit int
	sent  int
	batch []SearchHit
	last  time.Time
	seen  map[string]bool
}

func newSearchSink(run *SearchRun, emit func([]SearchHit), limit int) *searchSink {
	return &searchSink{run: run, emit: emit, limit: limit, seen: map[string]bool{}}
}

func (w *searchSink) stopped() bool { return w.run.canceled.Load() }

func (w *searchSink) add(h SearchHit) {
	if w.stopped() || h.Path == "" || w.sent+len(w.batch) >= w.limit {
		return
	}
	if w.seen[h.Path] {
		return
	}
	w.seen[h.Path] = true
	w.batch = append(w.batch, h)
	if len(w.batch) >= searchBatchMax || time.Since(w.last) >= searchBatchEvery {
		w.flush(false)
	}
}

// flush hands the batch over; force flushes a short one too.
func (w *searchSink) flush(force bool) {
	if len(w.batch) == 0 || w.emit == nil {
		return
	}
	if !force && len(w.batch) < searchBatchMax && time.Since(w.last) < searchBatchEvery {
		return
	}
	if w.stopped() {
		w.batch = nil
		return
	}
	out := w.batch
	w.batch = nil
	w.sent += len(out)
	w.last = time.Now()
	w.emit(out)
}

// --- the index --------------------------------------------------------------

// Windows Search's provider and the OLE DB interfaces the query needs.
var (
	clsidSearchCollator = windows.GUID{
		Data1: 0x9E175B8B, Data2: 0xF52A, Data3: 0x11D8,
		Data4: [8]byte{0xB9, 0xA5, 0x50, 0x50, 0x54, 0x50, 0x30, 0x30},
	}
	iidIDBInitialize = windows.GUID{
		Data1: 0x0C733A8B, Data2: 0x2A1C, Data3: 0x11CE,
		Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D},
	}
	iidIDBCreateSession = windows.GUID{
		Data1: 0x0C733A5D, Data2: 0x2A1C, Data3: 0x11CE,
		Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D},
	}
	iidIDBCreateCommand = windows.GUID{
		Data1: 0x0C733A1D, Data2: 0x2A1C, Data3: 0x11CE,
		Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D},
	}
	iidICommandText = windows.GUID{
		Data1: 0x0C733A27, Data2: 0x2A1C, Data3: 0x11CE,
		Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D},
	}
	iidIAccessor = windows.GUID{
		Data1: 0x0C733A8C, Data2: 0x2A1C, Data3: 0x11CE,
		Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D},
	}
	iidIRowset = windows.GUID{
		Data1: 0x0C733A7C, Data2: 0x2A1C, Data3: 0x11CE,
		Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D},
	}
	dbguidSQL = windows.GUID{
		Data1: 0xC8B521FB, Data2: 0x5CF3, Data3: 0x11CE,
		Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D},
	}
)

var errNoSearchIndex = stringError("platform: the Windows search index did not answer")

// stringError is a package-local error with a constant message.
type stringError string

func (e stringError) Error() string { return string(e) }

// dbBinding is DBBINDING on a 64-bit build, in the header's field order.
type dbBinding struct {
	iOrdinal   uintptr
	obValue    uintptr
	obLength   uintptr
	obStatus   uintptr
	pTypeInfo  uintptr
	pObject    uintptr
	pBindExt   uintptr
	dwPart     uint32
	dwMemOwner uint32
	eParamIO   uint32
	cbMaxLen   uintptr
	dwFlags    uint32
	wType      uint16
	bPrecision byte
	bScale     byte
}

const (
	dbaccessorRowdata = 2
	dbpartValue       = 1
	dbpartLength      = 2
	dbpartStatus      = 4
	dbtypeWStr        = 130
	dbtypeUI4         = 19
	dbtypeFiletime    = 64
	dbstatusOK        = 0
	searchColW        = 1024 // bytes of a bound string column
	// filetimeUnixBase is the 100ns ticks between 1601 and 1970.
	filetimeUnixBase = 116444736000000000
)

// searchSQL builds the index query for term. The name match is a LIKE
// over the display name; the content match is a full-text CONTAINS. A
// term carrying LIKE metacharacters (% _ [) skips the name match — the
// provider has no ESCAPE clause — and relies on CONTAINS, which reads
// them literally. A term carrying characters the full-text phrase syntax
// cannot quote skips the content match; a term carrying both is left to
// the folder scan. Both clauses sit inside SQL string literals, so a
// single quote is doubled in the term either way: the CONTAINS phrase's
// own quote layer is not a SQL layer.
func searchSQL(term string) (string, bool) {
	term = strings.TrimSpace(term)
	if term == "" {
		return "", false
	}
	esc := strings.ReplaceAll(term, "'", "''")
	var clauses []string
	if !strings.ContainsAny(term, "%_[") {
		// A SQL string literal, where a quote is doubled.
		clauses = append(clauses, `"System.ItemNameDisplay" LIKE '%`+esc+`%'`)
	}
	if !strings.ContainsAny(term, "\"\\") {
		// A full-text phrase, where a single quote is literal to the
		// phrase syntax but still ends the outer SQL string literal.
		clauses = append(clauses, `CONTAINS(*, '"`+esc+`*"')`)
	}
	if len(clauses) == 0 {
		return "", false
	}
	return `SELECT TOP ` + strconv.Itoa(searchLimit) +
		` "System.ItemUrl","System.ItemNameDisplay","System.FileAttributes","System.DateAccessed"` +
		` FROM "SYSTEMINDEX" WHERE (` + strings.Join(clauses, " OR ") + `)`, true
}

// searchIndex runs term against the index. A non-nil error means the
// provider could not answer at all and the caller should scan folders.
func searchIndex(term string, scope SearchScope, w *searchSink) error {
	sql, ok := searchSQL(term)
	if !ok {
		return errNoSearchIndex
	}
	cmdText := newSearchProvider()
	if cmdText == nil {
		return errNoSearchIndex
	}
	sql16 := windows.StringToUTF16(sql)
	hr := winCOMCall(cmdText, 7, uintptr(unsafe.Pointer(&dbguidSQL)), uintptr(unsafe.Pointer(&sql16[0])))
	runtime.KeepAlive(sql16)
	if failedHR(hr) {
		winCOMRelease(cmdText)
		return errNoSearchIndex
	}
	var rowset unsafe.Pointer
	hr = winCOMCall(cmdText, 4, 0, uintptr(unsafe.Pointer(&iidIRowset)), 0, 0, uintptr(unsafe.Pointer(&rowset)))
	winCOMRelease(cmdText)
	if failedHR(hr) || rowset == nil {
		return errNoSearchIndex
	}
	defer winCOMRelease(rowset)
	acc := winCOMQI(rowset, &iidIAccessor)
	if acc == nil {
		return errNoSearchIndex
	}
	defer winCOMRelease(acc)
	bindings, rowSize := searchBindings()
	var accessor uintptr
	hr = winCOMCall(acc, 4, dbaccessorRowdata, uintptr(len(bindings)),
		uintptr(unsafe.Pointer(&bindings[0])), rowSize, uintptr(unsafe.Pointer(&accessor)), 0)
	if failedHR(hr) || accessor == 0 {
		return errNoSearchIndex
	}
	buf := make([]byte, rowSize)
	const rowsPerFetch = 32
	for {
		if w.stopped() {
			return nil
		}
		// The provider allocates the handle array and hands it back; the
		// consumer frees it after releasing the rows.
		var fetched uintptr
		var handles unsafe.Pointer
		hr = winCOMCall(rowset, 5, 0, 0, rowsPerFetch, uintptr(unsafe.Pointer(&fetched)), uintptr(unsafe.Pointer(&handles)))
		done, err := searchBatchOutcome(hr, fetched, handles)
		if err != nil {
			// The provider failed while fetching: an empty answer here
			// would look like "no matches" and the callers would never
			// scan the common folders.
			if handles != nil {
				windows.CoTaskMemFree(handles)
			}
			return err
		}
		if done {
			return nil
		}
		rows := (*[rowsPerFetch]uintptr)(handles)
		for i := range fetched {
			if hr := winCOMCall(rowset, 4, rows[i], accessor, uintptr(unsafe.Pointer(&buf[0]))); failedHR(hr) {
				continue
			}
			if hit, ok := searchRowHit(buf, bindings, scope); ok {
				w.add(hit)
			}
		}
		for i := range fetched {
			if hr := winCOMCall(rowset, 6, 1, uintptr(unsafe.Pointer(&rows[i])), 0, 0, 0); failedHR(hr) {
				break
			}
		}
		windows.CoTaskMemFree(handles)
	}
}

// searchBatchOutcome classifies one IRowset::GetNextRows result. done is
// the end of the rows; a non-nil error is a provider failure, which the
// caller must answer by scanning the common folders instead of presenting
// a short list as the whole answer.
func searchBatchOutcome(hr, fetched uintptr, handles unsafe.Pointer) (done bool, err error) {
	if failedHR(hr) {
		return false, errNoSearchIndex
	}
	return fetched == 0 || handles == nil, nil
}

// searchBindings lays the four columns out in one row buffer and returns
// its size.
func searchBindings() ([]dbBinding, uintptr) {
	types := []uint16{dbtypeWStr, dbtypeWStr, dbtypeUI4, dbtypeFiletime}
	widths := []uintptr{searchColW, searchColW, 4, 8}
	bindings := make([]dbBinding, len(types))
	off := uintptr(0)
	for i := range bindings {
		b := dbBinding{
			iOrdinal: uintptr(i + 1),
			obValue:  off,
			obLength: off + widths[i],
			obStatus: off + widths[i] + 8,
			dwPart:   dbpartValue | dbpartLength | dbpartStatus,
			wType:    types[i],
		}
		if types[i] == dbtypeWStr {
			b.cbMaxLen = searchColW
		}
		bindings[i] = b
		off += widths[i] + 16
	}
	return bindings, off
}

// searchRowHit decodes one row into a hit.
func searchRowHit(buf []byte, bindings []dbBinding, scope SearchScope) (SearchHit, bool) {
	str := func(b dbBinding) string {
		if *(*uint32)(unsafe.Pointer(&buf[b.obStatus])) != dbstatusOK {
			return ""
		}
		var u []uint16
		for i := uintptr(0); i+1 < searchColW; i += 2 {
			c := uint16(buf[b.obValue+i]) | uint16(buf[b.obValue+i+1])<<8
			if c == 0 {
				break
			}
			u = append(u, c)
		}
		return strings.TrimSpace(windows.UTF16ToString(u))
	}
	rawURL, name := str(bindings[0]), str(bindings[1])
	if rawURL == "" {
		return SearchHit{}, false
	}
	path := searchPathFromURL(rawURL)
	if !searchPathWanted(path) {
		return SearchHit{}, false
	}
	if name == "" {
		name = filepath.Base(path)
	}
	hit := SearchHit{Path: path, Name: name}
	if *(*uint32)(unsafe.Pointer(&buf[bindings[2].obStatus])) == dbstatusOK {
		attrs := *(*uint32)(unsafe.Pointer(&buf[bindings[2].obValue]))
		hit.Folder = attrs&0x10 != 0
	}
	if *(*uint32)(unsafe.Pointer(&buf[bindings[3].obStatus])) == dbstatusOK {
		// System.DateAccessed is a FILETIME: 100ns units since 1601.
		ft := *(*int64)(unsafe.Pointer(&buf[bindings[3].obValue]))
		if ft > filetimeUnixBase {
			hit.LastUsed = time.Unix(0, (ft-filetimeUnixBase)*100)
		}
	}
	// A Start Menu shortcut is an application the Apps folder already
	// lists with its display name and icon; the index's copy (named
	// "App.lnk") would only be a duplicate row.
	if isStartMenuShortcut(path) {
		return SearchHit{}, false
	}
	if !scope.wants(hit) {
		return SearchHit{}, false
	}
	return hit, true
}

// newSearchProvider opens the Search provider's command text interface:
// initialize, create a session, create a command, ask for its text
// interface. Each intermediate object is released as soon as the next one
// holds what the query needs.
func newSearchProvider() unsafe.Pointer {
	dbinit := winCoCreateInstance(&clsidSearchCollator, &iidIDBInitialize)
	if dbinit == nil {
		return nil
	}
	defer winCOMRelease(dbinit)
	if failedHR(winCOMCall(dbinit, 3)) {
		return nil
	}
	session := winCOMQI(dbinit, &iidIDBCreateSession)
	if session == nil {
		return nil
	}
	defer winCOMRelease(session)
	var createCmd unsafe.Pointer
	if failedHR(winCOMCall(session, 3, 0, uintptr(unsafe.Pointer(&iidIDBCreateCommand)), uintptr(unsafe.Pointer(&createCmd)))) || createCmd == nil {
		return nil
	}
	defer winCOMRelease(createCmd)
	var cmdText unsafe.Pointer
	if failedHR(winCOMCall(createCmd, 3, 0, uintptr(unsafe.Pointer(&iidICommandText)), uintptr(unsafe.Pointer(&cmdText)))) || cmdText == nil {
		return nil
	}
	return cmdText
}

// searchPathFromURL turns System.ItemUrl ("file:C:/Users/…") into a
// Win32 path. A URL the shell could open but the file system cannot (a
// library or a search connector) is not a file hit.
func searchPathFromURL(raw string) string {
	if !strings.HasPrefix(raw, "file:") {
		return ""
	}
	p := strings.TrimPrefix(raw, "file:")
	// ItemUrl leaves spaces raw and percent-encodes the rest.
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	p = strings.TrimLeft(p, "/")
	p = strings.ReplaceAll(p, "/", `\`)
	if len(p) >= 2 && p[1] == ':' {
		p = strings.ToUpper(p[:2]) + p[2:]
	}
	return p
}

// isStartMenuShortcut reports whether path is a Start Menu entry, the
// way macOS names an installed application bundle: only the shortcuts in
// the Start Menu trees count as applications.
func isStartMenuShortcut(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".lnk") {
		return false
	}
	lower := strings.ToLower(path)
	return strings.Contains(lower, `\start menu\programs\`)
}

// searchPathWanted applies the results' visibility rule: not a
// temporary file, not inside Windows, not in a build or package cache.
func searchPathWanted(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	lower := strings.ToLower(path)
	for _, bad := range []string{
		`\appdata\`,
		`\$recycle.bin\`,
		`\node_modules\`,
		`\system volume information\`,
	} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	// The Windows directory itself, not every folder that mentions it:
	// ProgramData\Microsoft\Windows\Start Menu holds the applications.
	if root, err := os.Getenv("SystemRoot"), error(nil); err == nil && root != "" {
		if strings.HasPrefix(lower, strings.ToLower(filepath.Clean(root))+`\`) {
			return false
		}
	}
	base := filepath.Base(lower)
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, ".tmp") {
		return false
	}
	for _, part := range strings.Split(lower, `\`) {
		if part == "cache" || part == "caches" || part == ".git" {
			return false
		}
	}
	return true
}

// --- the folder scan fallback ----------------------------------------------

// searchCommonFolders lists the user's own folders, the roots the folder
// scan falls back to when the index cannot answer.
func searchCommonFolders() []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range []windows.GUID{
		{Data1: 0xB4BFCC3A, Data2: 0xDB2C, Data3: 0x424C, Data4: [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}}, // Desktop
		{Data1: 0xFDD39AD0, Data2: 0x238F, Data3: 0x46AF, Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7}}, // Documents
		{Data1: 0x374DE290, Data2: 0x123F, Data3: 0x4565, Data4: [8]byte{0x91, 0x64, 0x39, 0xC4, 0x92, 0x5E, 0x46, 0x7B}}, // Downloads
		{Data1: 0x33E28130, Data2: 0x4E1E, Data3: 0x4676, Data4: [8]byte{0x83, 0x5A, 0x98, 0x39, 0x5C, 0x3B, 0xC3, 0xBB}}, // Pictures
		{Data1: 0x4BD8D571, Data2: 0x6D19, Data3: 0x48D3, Data4: [8]byte{0xBE, 0x97, 0x42, 0x22, 0x20, 0x08, 0x0E, 0x43}}, // Music
		{Data1: 0x18989B1D, Data2: 0x99B5, Data3: 0x455B, Data4: [8]byte{0x84, 0x1C, 0xAB, 0x7C, 0x74, 0xE4, 0xDD, 0xFC}}, // Videos
	} {
		p := knownFolderPath(&id)
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	return out
}

const (
	searchScanDepth = 8
	searchScanFiles = 4000
)

// searchScan walks the common folders for names containing term.
func searchScan(term string, scope SearchScope, w *searchSink) {
	lower := strings.ToLower(term)
	visited := 0
	for _, root := range searchCommonFolders() {
		if w.stopped() || visited >= searchScanFiles {
			return
		}
		baseDepth := strings.Count(root, `\`)
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if w.stopped() || visited >= searchScanFiles || w.sent+len(w.batch) >= w.limit {
				return filepath.SkipAll
			}
			visited++
			name := d.Name()
			if d.IsDir() && path != root && searchSkipDir(name) {
				return filepath.SkipDir
			}
			if d.IsDir() && path != root && strings.Count(path, `\`)-baseDepth >= searchScanDepth {
				return filepath.SkipDir
			}
			if name == "" || hiddenOrSystem(path) {
				return nil
			}
			if !strings.Contains(strings.ToLower(name), lower) {
				return nil
			}
			hit := SearchHit{Path: path, Name: name, Folder: d.IsDir()}
			if !scope.wants(hit) {
				return nil
			}
			if info, err := d.Info(); err == nil {
				hit.LastUsed = info.ModTime()
			}
			w.add(hit)
			return nil
		})
	}
}

// searchSkipDir names the directories a name scan never enters.
func searchSkipDir(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, ".") {
		return true
	}
	switch lower {
	case "appdata", "node_modules", "$recycle.bin", "system volume information",
		"windows", "cache", "caches", "tmp", "temp":
		return true
	}
	return false
}

// hiddenOrSystem reports the attributes that keep a file out of results.
func hiddenOrSystem(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	const (
		fileAttributeHidden = 0x2
		fileAttributeSystem = 0x4
	)
	return attrs&(fileAttributeHidden|fileAttributeSystem) != 0
}

// --- app list ---------------------------------------------------------------

// appEntry is one row of the shell's Apps folder.
type appEntry struct {
	Name   string
	Target string
}

var appList struct {
	mu   sync.Mutex
	at   time.Time
	list []appEntry
}

// appListFresh returns the Apps folder entries, re-enumerating them when
// the cache is a minute old. A failed enumeration keeps the last list.
func appListFresh() []appEntry {
	appList.mu.Lock()
	defer appList.mu.Unlock()
	if time.Since(appList.at) < searchAppMaxAge {
		return appList.list
	}
	if list, err := appsFolderEntries(); err == nil {
		appList.list = list
		appList.at = time.Now()
	}
	return appList.list
}

// appListName returns the display name of the Apps folder entry an
// identity names.
func appListName(id string) (string, bool) {
	for _, e := range appListFresh() {
		if e.Target == id || e.Target == `shell:AppsFolder\`+id {
			return e.Name, true
		}
	}
	return "", false
}
