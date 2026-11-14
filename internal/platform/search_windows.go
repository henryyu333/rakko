//go:build windows

package platform

// Spotlight-equivalent search. The skeleton answers an empty result set so
// the bar and its result sheet work; the Windows index reader and the
// common-folder fallback arrive with the search batch.
// WINDOWS-NEXT(search)

import (
	"sync/atomic"
	"time"
)

// SearchScope is what a search looks at.
type SearchScope int

const (
	ScopeAll   SearchScope = iota // names and text content, everything
	ScopeApps                     // application entries, by name only
	ScopeFiles                    // names and text content, bundles left out
)

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

// StartSearch runs term. WINDOWS-NEXT(search): no index is consulted yet,
// so the query finishes at once with no hits.
func StartSearch(term string, scope SearchScope, limit int, emit func([]SearchHit), done func()) *SearchRun {
	r := &SearchRun{}
	if done != nil {
		// The caller's UI state lives on the main thread; deliver the end
		// of the query there as the real index would.
		WakeMain(done)
	}
	return r
}

// Cancel stops the run.
func (r *SearchRun) Cancel() {
	if r != nil {
		r.canceled.Store(true)
	}
}

// WarmSearch primes the index. WINDOWS-NEXT(search)
func WarmSearch() {}
