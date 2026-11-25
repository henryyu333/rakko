package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
	"rakko/locales"
)

// TestBoardStress1500PlusPerf builds a 1,560+ element board (fountain-pen strokes,
// geo shapes, curved arrows, and Chinese/English handwritten text), drives headless
// render, pan, zoom, live stroke input, and tiled PNG export, and reports raw
// frame timing, heap memory, and mask-atlas budget degradation metrics.
func TestBoardStress1500PlusPerf(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1280, 800)
	view.SetLocale("zh-Hans")

	const (
		numDraws  = 1200
		numGeos   = 200
		numArrows = 100
		numTexts  = 60
		totalInit = numDraws + numGeos + numArrows + numTexts // 1560
	)

	var simTime float64
	b.ed.ToolLocked = true
	b.ed.SetTool(editor.ToolDraw)
	b.ed.Next.PenKind = "pen"
	b.ed.Next.PenWidth = 3
	for i := 0; i < numDraws; i++ {
		col := float64(i%40)*42 + 40
		row := float64(i/40)*26 + 40
		b.ed.Next.Stroke = editor.Swatches[i%len(editor.Swatches)]
		b.ed.PointerDown(col, row, 0, 1, editor.Mods{}, simTime)
		b.ed.PointerMove(col+90, row-12, editor.Mods{}, simTime+0.005)
		b.ed.PointerMove(col+160, row+18, editor.Mods{}, simTime+0.010)
		b.ed.PointerUp(col+160, row+18, editor.Mods{}, simTime+0.015)
		simTime += 0.02
	}

	geos := []string{"rectangle", "ellipse", "diamond", "triangle", "hexagon", "star"}
	for i := 0; i < numGeos; i++ {
		col := float64(i%20)*85 + 60
		row := float64(i/20)*75 + 60
		b.ed.SetGeoTool(geos[i%len(geos)])
		b.ed.Next.Stroke = editor.Swatches[(i+3)%len(editor.Swatches)]
		b.ed.Next.Fill = "semi"
		b.ed.PointerDown(col, row, 0, 1, editor.Mods{}, simTime)
		b.ed.PointerMove(col+56, row+44, editor.Mods{}, simTime+0.005)
		b.ed.PointerUp(col+56, row+44, editor.Mods{}, simTime+0.010)
		simTime += 0.02
	}

	b.ed.SetTool(editor.ToolArrow)
	for i := 0; i < numArrows; i++ {
		col := float64(i%10)*150 + 80
		row := float64(i/10)*70 + 80
		b.ed.Next.Stroke = editor.Swatches[(i+6)%len(editor.Swatches)]
		b.ed.PointerDown(col, row, 0, 1, editor.Mods{}, simTime)
		b.ed.PointerMove(col+95, row+28, editor.Mods{}, simTime+0.005)
		b.ed.PointerUp(col+95, row+28, editor.Mods{}, simTime+0.010)
		simTime += 0.02
	}

	b.ed.Next.Font = "draw"
	b.ed.Next.FontSize = 18
	for i := 0; i < numTexts; i++ {
		col := float64(i%6)*250 + 100
		row := float64(i/6)*80 + 50
		b.ed.CreateText(geo.V(col, row), fmt.Sprintf("手写笔记 #%d Note", i+1))
	}

	b.ed.ToolLocked = false
	b.ed.SetTool(editor.ToolSelect)
	b.ed.SelectNone()

	if got := b.ed.ItemCount(); got != totalInit {
		t.Fatalf("ItemCount = %d, want %d", got, totalInit)
	}

	runtime.GC()
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Warmup frames
	view.Frame()
	view.Frame()
	basePix, baseRect := boardToolbarPixels(t, view, l)

	measure := func(n int, step func(i int)) []time.Duration {
		out := make([]time.Duration, n)
		for i := 0; i < n; i++ {
			t0 := time.Now()
			if step != nil {
				step(i)
			}
			view.Frame()
			out[i] = time.Since(t0)
		}
		return out
	}

	idleTimes := measure(5, nil)
	zoomTimes := measure(5, func(i int) {
		view.Pinch(640, 400, 1.06)
	})
	panTimes := measure(5, func(i int) {
		view.Wheel(640, 400, 40, -24)
	})
	drawTimes := measure(3, func(i int) {
		if i == 0 {
			b.ed.SetTool(editor.ToolDraw)
			b.ed.Next.PenKind = "pen"
			b.ed.PointerDown(200, 200, 0, 1, editor.Mods{}, simTime)
		} else if i == 1 {
			b.ed.PointerMove(420, 280, editor.Mods{}, simTime+0.01)
		} else {
			b.ed.PointerUp(420, 280, editor.Mods{}, simTime+0.02)
		}
	})

	runtime.ReadMemStats(&memAfter)

	stats := func(d []time.Duration) (minD, meanD, p95D, maxD time.Duration) {
		s := slices.Clone(d)
		slices.Sort(s)
		var sum time.Duration
		for _, v := range s {
			sum += v
		}
		p95Idx := (len(s)*95 - 1) / 100
		return s[0], sum / time.Duration(len(s)), s[p95Idx], s[len(s)-1]
	}

	iMin, iMean, iP95, iMax := stats(idleTimes)
	zMin, zMean, zP95, zMax := stats(zoomTimes)
	pMin, pMean, pP95, pMax := stats(panTimes)
	dMin, dMean, dP95, dMax := stats(drawTimes)

	const mygoMaskAtlasMax = 4096.0 * 4096.0
	if b.lastMaskArea <= 0 || b.lastMaskArea >= mygoMaskAtlasMax {
		t.Fatalf("estMaskArea = %.0f out of realistic atlas bounds (0, %.0f)", b.lastMaskArea, mygoMaskAtlasMax)
	}
	if b.lastMaskArea > boardAtlasBudget && b.lastDegradedCount == 0 {
		t.Fatalf("estMaskArea %.0f exceeded budget %.0f without degrading any shape", b.lastMaskArea, boardAtlasBudget)
	}
	if b.lastMaskArea <= boardAtlasBudget && b.lastDegradedCount != 0 {
		t.Fatalf("estMaskArea %.0f within budget %.0f unexpectedly degraded %d shapes", b.lastMaskArea, boardAtlasBudget, b.lastDegradedCount)
	}
	if b.ed.ItemCount() != totalInit+1 {
		t.Fatalf("ItemCount after live draw = %d, want %d", b.ed.ItemCount(), totalInit+1)
	}
	b.ed.SetTool(editor.ToolSelect)
	view.Frame()
	afterPix, afterRect := boardToolbarPixels(t, view, l)
	if afterRect != baseRect {
		t.Fatalf("toolbar rect moved under stress: %v -> %v", baseRect, afterRect)
	}
	if diff := boardPixelsDiffer(basePix, afterPix, 24); diff > 0 {
		t.Fatalf("toolbar pixels changed under stress (%d pixels)", diff)
	}
	// Verify rendered frame has non-background content across all 4 canvas quadrants (no missing shapes).
	stressImg := view.Image()
	bgCol := boardColorRGBA(b.ed.Palette().Background)
	qBoxes := []ui.Rect{
		{X: 40, Y: boardBarH + 20, W: 560, H: 320},
		{X: 640, Y: boardBarH + 20, W: 560, H: 320},
		{X: 40, Y: boardBarH + 360, W: 560, H: 280},
		{X: 640, Y: boardBarH + 360, W: 560, H: 280},
	}
	for qi, qb := range qBoxes {
		nonBG := 0
		for y := int(qb.Y); y < int(qb.Y+qb.H); y += 2 {
			for x := int(qb.X); x < int(qb.X+qb.W); x += 2 {
				c := stressImg.RGBAAt(x, y)
				if c.R != bgCol.R || c.G != bgCol.G || c.B != bgCol.B {
					nonBG++
				}
			}
		}
		if nonBG < 100 {
			t.Fatalf("quadrant %d has too few drawn pixels (%d); possible missing shapes", qi, nonBG)
		}
	}
	if err := view.Click(l.BoardToolArrow()); err != nil {
		t.Fatalf("toolbar click failed under stress: %v", err)
	}
	view.Frame()
	if b.ed.Tool != editor.ToolArrow {
		t.Fatalf("expected Arrow tool after stress click, got %s", b.ed.Tool)
	}

	saveTestShot(t, "seg9", "01-stress-1560-board.png", view.Image())

	exportT0 := time.Now()
	pngBytes, expW, expH, tiles, err := b.renderTiledPNG(nil)
	exportDur := time.Since(exportT0)
	if err != nil {
		t.Fatalf("renderTiledPNG(nil) under stress: %v", err)
	}
	if len(pngBytes) == 0 || expW <= 0 || expH <= 0 || tiles <= 0 {
		t.Fatalf("invalid stress PNG export: bytes=%d w=%d h=%d tiles=%d", len(pngBytes), expW, expH, tiles)
	}

	t.Logf("STRESS_ITEMS=%d (draws=%d+1, geos=%d, arrows=%d, texts=%d) lightInk=%v degraded=%d estMaskArea=%.0f budget=%.0f",
		b.ed.ItemCount(), numDraws, numGeos, numArrows, numTexts, b.lastLightInk, b.lastDegradedCount, b.lastMaskArea, boardAtlasBudget)
	t.Logf("FRAME_MS idle(min=%.2f mean=%.2f p95=%.2f max=%.2f) zoom(min=%.2f mean=%.2f p95=%.2f max=%.2f) pan(min=%.2f mean=%.2f p95=%.2f max=%.2f) draw(min=%.2f mean=%.2f p95=%.2f max=%.2f)",
		float64(iMin.Microseconds())/1000, float64(iMean.Microseconds())/1000, float64(iP95.Microseconds())/1000, float64(iMax.Microseconds())/1000,
		float64(zMin.Microseconds())/1000, float64(zMean.Microseconds())/1000, float64(zP95.Microseconds())/1000, float64(zMax.Microseconds())/1000,
		float64(pMin.Microseconds())/1000, float64(pMean.Microseconds())/1000, float64(pP95.Microseconds())/1000, float64(pMax.Microseconds())/1000,
		float64(dMin.Microseconds())/1000, float64(dMean.Microseconds())/1000, float64(dP95.Microseconds())/1000, float64(dMax.Microseconds())/1000)
	t.Logf("MEMORY heapAlloc=%.2fMB heapInuse=%.2fMB totalAllocDelta=%.2fMB numGCDelta=%d",
		float64(memAfter.HeapAlloc)/(1024*1024),
		float64(memAfter.HeapInuse)/(1024*1024),
		float64(memAfter.TotalAlloc-memBefore.TotalAlloc)/(1024*1024),
		memAfter.NumGC-memBefore.NumGC)
	t.Logf("EXPORT_TILED_PNG size=%dx%d tiles=%d bytes=%d durMs=%.2f",
		expW, expH, tiles, len(pngBytes), float64(exportDur.Microseconds())/1000)
}

// TestBoardSaveBenchmark reports the main-thread save cost, board size, and
// five-board List cost for the 1500-stroke board.
func TestBoardSaveBenchmark(t *testing.T) {
	_, b := boardTestShelf(t)
	const strokes = 1500
	for i := 0; i < strokes; i++ {
		id := doc.NewRecordID("shape")
		s := doc.NewShape(id, doc.TypeDraw)
		s.Parent = b.ed.Page
		s.Index = doc.IndexBetween("", "")
		if i > 0 {
			prev := b.ed.Doc.Shapes[doc.ID("shape:bench"+strconv.Itoa(i-1))]
			if prev != nil {
				s.Index = doc.IndexBetween(prev.Index, "")
			}
		}
		// Keep a deterministic, lookup-friendly id.
		s.ID = doc.ID("shape:bench" + strconv.Itoa(i))
		pts := make([]doc.PointZ, 30)
		for k := range pts {
			pts[k] = doc.PointZ{X: float64(i)*3 + float64(k), Y: float64(i%400) + float64(k)*0.5, Z: 0.5}
		}
		s.Segments = []doc.Segment{{Type: "free", Points: pts}}
		s.IsComplete = true
		b.ed.Doc.Shapes[s.ID] = s
	}
	var mainNs []time.Duration
	for i := 0; i < 5; i++ {
		b.dirtyFlag = true
		t0 := time.Now()
		if err := b.save(); err != nil {
			t.Fatal(err)
		}
		mainNs = append(mainNs, time.Since(t0))
		if err := b.waitSaves(); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(mainNs, func(i, j int) bool { return mainNs[i] < mainNs[j] })
	info, err := os.Stat(b.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := b.store.New("Bench " + strconv.Itoa(i+2)); err != nil {
			t.Fatal(err)
		}
	}
	var listNs []time.Duration
	for i := 0; i < 5; i++ {
		t0 := time.Now()
		if _, err := b.store.List(); err != nil {
			t.Fatal(err)
		}
		listNs = append(listNs, time.Since(t0))
	}
	sort.Slice(listNs, func(i, j int) bool { return listNs[i] < listNs[j] })
	t.Logf("BENCH flush_main median=%v size=%dB list5 median=%v",
		mainNs[2], info.Size(), listNs[2])
}
