package geo

import (
	"math"
	"strings"
	"testing"
)

func almostEq(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}

func TestVecAndBoxOps(t *testing.T) {
	a, b := V(3, 4), V(-1, 2)
	if !almostEq(a.Len(), 5, 1e-9) {
		t.Fatalf("Len = %v, want 5", a.Len())
	}
	if a.Add(b) != V(2, 6) || a.Sub(b) != V(4, 2) {
		t.Fatalf("Add/Sub mismatch")
	}
	if u := a.Unit(); !almostEq(u.Len(), 1, 1e-9) {
		t.Fatalf("Unit len = %v", u.Len())
	}
	if (Vec{}).Unit() != (Vec{}) || a.Div(0) != (Vec{}) {
		t.Fatalf("zero vector division failed")
	}
	if r := V(1, 0).Rot(math.Pi / 2); !almostEq(r.X, 0, 1e-9) || !almostEq(r.Y, 1, 1e-9) {
		t.Fatalf("Rot = %+v", r)
	}

	bx := BoxFrom(V(10, 20), V(2, 5))
	if bx != (Box{2, 5, 8, 15}) {
		t.Fatalf("BoxFrom = %+v", bx)
	}
	if !bx.Contains(V(5, 10)) || bx.Contains(V(1, 10)) {
		t.Fatalf("Contains failed")
	}
	u := bx.Union(Box{0, 0, 4, 4})
	if u != (Box{0, 0, 10, 20}) {
		t.Fatalf("Union = %+v", u)
	}
}

func TestMatComposeAndInvert(t *testing.T) {
	m := Compose(100, 50, math.Pi/3).Mul(Scale(2, 3))
	inv := m.Invert()
	p := V(13, -7)
	rt := inv.Apply(m.Apply(p))
	if rt.Dist(p) > 1e-9 {
		t.Fatalf("roundtrip = %+v, want %+v", rt, p)
	}
	if (Mat{}).Invert() != Identity {
		t.Fatalf("singular invert should return Identity")
	}
}

func TestPathFlattenAndSVG(t *testing.T) {
	ep := EllipsePath(Box{0, 0, 100, 60})
	pts, closed := ep.Points(8)
	if !closed || len(pts) < 16 {
		t.Fatalf("EllipsePath flatten: closed=%v len=%d", closed, len(pts))
	}
	svg := ep.SVG(Identity)
	if !strings.HasPrefix(svg, "M") || !strings.Contains(svg, "C") || !strings.HasSuffix(svg, "Z") {
		t.Fatalf("unexpected SVG path: %q", svg)
	}

	rp := RoundedRectPath(Box{0, 0, 40, 20}, 6)
	rpts, rclosed := rp.Points(4)
	if !rclosed || len(rpts) < 8 {
		t.Fatalf("RoundedRectPath: closed=%v len=%d", rclosed, len(rpts))
	}
}

func TestIntersectionsAndPolygon(t *testing.T) {
	p, ok := SegmentsCross(V(0, 0), V(10, 10), V(0, 10), V(10, 0))
	if !ok || p.Dist(V(5, 5)) > 1e-9 {
		t.Fatalf("SegmentsCross = %+v, %v", p, ok)
	}
	if _, ok := SegmentsCross(V(0, 0), V(1, 0), V(0, 1), V(1, 1)); ok {
		t.Fatal("parallel segments reported crossing")
	}

	poly := []Vec{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	if !PointInPolygon(V(5, 5), poly) || PointInPolygon(V(15, 5), poly) {
		t.Fatal("PointInPolygon failed")
	}

	o, rad, ok := CircleFrom3Points(V(1, 0), V(0, 1), V(-1, 0))
	if !ok || o.Dist(V(0, 0)) > 1e-9 || !almostEq(rad, 1, 1e-9) {
		t.Fatalf("CircleFrom3Points = %+v, %v, %v", o, rad, ok)
	}
	if _, _, ok := CircleFrom3Points(V(0, 0), V(1, 1), V(2, 2)); ok {
		t.Fatal("collinear points formed a circle")
	}
}

func TestSimplifyAndPartialClip(t *testing.T) {
	line := []Vec{{0, 0}, {5, 0.01}, {10, 0}}
	simp := SimplifyRDP(line, 0.5)
	if len(simp) != 2 || simp[0] != (Vec{0, 0}) || simp[1] != (Vec{10, 0}) {
		t.Fatalf("SimplifyRDP = %+v", simp)
	}

	// Eraser disc in the middle splits a segment into two pieces.
	segs := ClipSegmentOutsideDiscs(V(0, 0), V(100, 0), []Vec{V(50, 0)}, 10)
	if len(segs) != 2 {
		t.Fatalf("expected 2 clipped segments, got %d: %+v", len(segs), segs)
	}
	if segs[0][0].Dist(V(0, 0)) > 1e-6 || segs[0][1].Dist(V(40, 0)) > 1e-6 {
		t.Fatalf("first piece = %+v", segs[0])
	}
	if segs[1][0].Dist(V(60, 0)) > 1e-6 || segs[1][1].Dist(V(100, 0)) > 1e-6 {
		t.Fatalf("second piece = %+v", segs[1])
	}

	// Eraser sweeping across middle of a polyline splits it into two polylines.
	poly := []Vec{{0, 0}, {50, 0}, {100, 0}}
	out := ClipPolylineOutsideCapsules(poly, []Vec{{50, -20}, {50, 20}}, 10)
	if len(out) != 2 {
		t.Fatalf("expected 2 surviving polylines, got %d: %+v", len(out), out)
	}
	// Eraser covering entire polyline removes it completely.
	outAll := ClipPolylineOutsideCapsules([]Vec{{45, 0}, {55, 0}}, []Vec{{50, 0}}, 20)
	if len(outAll) != 0 {
		t.Fatalf("expected 0 surviving polylines, got %+v", outAll)
	}
}

func TestDeterministicRngAndRibbon(t *testing.T) {
	r1, r2 := NewRng("shape:abc"), NewRng("shape:abc")
	for i := 0; i < 10; i++ {
		v1, v2 := r1.Next(), r2.Next()
		if v1 != v2 || v1 < -1 || v1 >= 1 {
			t.Fatalf("Rng mismatch/range: %v vs %v", v1, v2)
		}
	}

	pts := []StrokePoint{{0, 0, 0.5}, {20, 0, 0.8}, {40, 10, 0.5}, {60, 10, 0.3}}
	rib := RibbonOutline(pts, 6, false)
	if len(rib) != 2*len(pts) {
		t.Fatalf("RibbonOutline len = %d, want %d", len(rib), 2*len(pts))
	}
}
