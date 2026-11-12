//go:build darwin

package main

// Shared test seams.

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/mac"
	"rakko/internal/settings"
	"rakko/internal/store"
)

// panelTestH is a headless panel surface's height: the sheet and the page
// capsule hung under it, so a test can click the capsule's page buttons.
const panelTestH = sheetH + navGap + navH + 2

// toolsTester is a headless surface at the tools page's current shell box,
// plus the ear corners and the page capsule hung under it. The page hugs
// its cards, so the surface has to be built after whatever state changes
// its packing (a card added, editing on) — a fixed 440 × 298 surface would
// clip a wider or taller panel.
func toolsTester(a *shelf) *ui.Tester {
	w := int(math.Ceil(float64(a.pageShellW()))) + 2*earR
	h := int(math.Ceil(float64(a.pageShellH()))) + navGap + navH + 2
	return ui.NewTester(a.view, w, h)
}

// fakeVolume is the replaceable volume seam: tests never touch the
// machine's real output volume.
type fakeVolume struct {
	v    float32
	ok   bool
	err  error
	sets []float32
}

func (f *fakeVolume) level() (float32, bool) { return f.v, f.ok }
func (f *fakeVolume) set(v float32) error {
	f.sets = append(f.sets, v)
	if f.err != nil {
		return f.err
	}
	f.v = v
	return nil
}

// musicShelf builds a shelf with a real track and a page tester: the home
// page's player controls live in a real layout.
func musicShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{store: s, dataDir: t.TempDir()}
	a.vol = &fakeVolume{v: 0.4, ok: true}
	a.np = &nowPlayingModule{
		host: a, have: true,
		last: mac.NowPlayingState{
			Title: "座右铭", Artist: "吴雨霏", Duration: 200, Elapsed: 20,
			Rate: 1, Received: time.Now(),
		},
	}
	a.setTab(pageHome)
	view := ui.NewTester(a.view, sheetW, panelTestH)
	return a, view
}

// cardByID finds a card in the grid.
func cardByID(cards []layout.Card, id string) (layout.Card, bool) {
	for _, c := range cards {
		if c.ID == id {
			return c, true
		}
	}
	return layout.Card{}, false
}

// pinWindowRect is a convenient empty frame for pin seams in tests.
func pinWindowRect() mygo.Rectangle { return mygo.Rectangle{Width: 200, Height: 100} }

// settingsForTest is the default settings without touching the disk.
func settingsForTest() settings.Settings { return settings.Default() }
