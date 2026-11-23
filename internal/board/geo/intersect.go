package geo

import (
	"math"
	"sort"
)

// NearestOnSegment returns the closest point on segment ab to p.
func NearestOnSegment(p, a, b Vec) Vec {
	ab := b.Sub(a)
	l2 := ab.Dot(ab)
	if l2 == 0 {
		return a
	}
	t := Clamp(p.Sub(a).Dot(ab)/l2, 0, 1)
	return a.Lerp(b, t)
}

// DistToSegment returns the Euclidean distance from p to segment ab.
func DistToSegment(p, a, b Vec) float64 {
	return p.Dist(NearestOnSegment(p, a, b))
}

// DistToPolyline returns the minimum distance from p to pts, closing the loop if closed.
func DistToPolyline(p Vec, pts []Vec, closed bool) float64 {
	if len(pts) == 0 {
		return math.Inf(1)
	}
	if len(pts) == 1 {
		return p.Dist(pts[0])
	}
	best := math.Inf(1)
	for i := 1; i < len(pts); i++ {
		best = math.Min(best, DistToSegment(p, pts[i-1], pts[i]))
	}
	if closed {
		best = math.Min(best, DistToSegment(p, pts[len(pts)-1], pts[0]))
	}
	return best
}

// PointInPolygon reports whether p is inside poly using the non-zero winding rule.
func PointInPolygon(p Vec, poly []Vec) bool {
	wn := 0
	n := len(poly)
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		isLeft := (b.X-a.X)*(p.Y-a.Y) - (p.X-a.X)*(b.Y-a.Y)
		if a.Y <= p.Y {
			if b.Y > p.Y && isLeft > 0 {
				wn++
			}
		} else if b.Y <= p.Y && isLeft < 0 {
			wn--
		}
	}
	return wn != 0
}

// SegmentsCross returns the intersection point of segments ab and cd, if they cross.
func SegmentsCross(a, b, c, d Vec) (Vec, bool) {
	r := b.Sub(a)
	s := d.Sub(c)
	den := r.Cross(s)
	if den == 0 {
		return Vec{}, false
	}
	ca := c.Sub(a)
	t := ca.Cross(s) / den
	u := ca.Cross(r) / den
	if t < 0 || t > 1 || u < 0 || u > 1 {
		return Vec{}, false
	}
	return a.Lerp(b, t), true
}

// SegmentPolyline returns all intersection points of segment ab with pts in polyline order.
func SegmentPolyline(a, b Vec, pts []Vec, closed bool) []Vec {
	n := len(pts)
	if n < 2 {
		return nil
	}
	last := n - 1
	if closed {
		last = n
	}
	var out []Vec
	for i := 0; i < last; i++ {
		if p, ok := SegmentsCross(a, b, pts[i], pts[(i+1)%n]); ok {
			out = append(out, p)
		}
	}
	return out
}

// SegmentHitsPolyline reports whether segment ab intersects or passes within margin of pts.
func SegmentHitsPolyline(a, b Vec, pts []Vec, closed bool, margin float64) bool {
	n := len(pts)
	if n == 0 {
		return false
	}
	if n == 1 {
		return DistToSegment(pts[0], a, b) <= margin
	}
	last := n - 1
	if closed {
		last = n
	}
	for i := 0; i < last; i++ {
		c, d := pts[i], pts[(i+1)%n]
		if _, ok := SegmentsCross(a, b, c, d); ok {
			return true
		}
		if margin > 0 {
			if DistToSegment(a, c, d) <= margin || DistToSegment(b, c, d) <= margin ||
				DistToSegment(c, a, b) <= margin || DistToSegment(d, a, b) <= margin {
				return true
			}
		}
	}
	return false
}

// BoxHitsPolyline reports whether polyline pts intersects or has any vertex inside b.
func BoxHitsPolyline(b Box, pts []Vec, closed bool) bool {
	for _, p := range pts {
		if b.Contains(p) {
			return true
		}
	}
	c := b.Corners()
	for i := 0; i < 4; i++ {
		if SegmentHitsPolyline(c[i], c[(i+1)%4], pts, closed, 0) {
			return true
		}
	}
	return false
}

// PolygonHitsPolyline reports whether closed polygon poly overlaps or intersects pts.
func PolygonHitsPolyline(poly, pts []Vec, closed bool) bool {
	if len(poly) < 3 || len(pts) == 0 {
		return false
	}
	for _, p := range pts {
		if PointInPolygon(p, poly) {
			return true
		}
	}
	if closed && len(pts) >= 3 {
		for _, p := range poly {
			if PointInPolygon(p, pts) {
				return true
			}
		}
	}
	for i := 0; i < len(poly); i++ {
		a, b := poly[i], poly[(i+1)%len(poly)]
		if SegmentHitsPolyline(a, b, pts, closed, 0) {
			return true
		}
	}
	return false
}

// CircleFrom3Points returns the circumcenter, radius, and true if a, b, c are non-collinear.
func CircleFrom3Points(a, b, c Vec) (center Vec, radius float64, ok bool) {
	d := 2 * (a.X*(b.Y-c.Y) + b.X*(c.Y-a.Y) + c.X*(a.Y-b.Y))
	if math.Abs(d) < 1e-6 {
		return Vec{}, 0, false
	}
	a2, b2, c2 := a.Dot(a), b.Dot(b), c.Dot(c)
	o := Vec{
		X: (a2*(b.Y-c.Y) + b2*(c.Y-a.Y) + c2*(a.Y-b.Y)) / d,
		Y: (a2*(c.X-b.X) + b2*(a.X-c.X) + c2*(b.X-a.X)) / d,
	}
	return o, o.Dist(a), true
}

// ShortAngle returns the shortest signed angular difference from a to b in [-π, π].
func ShortAngle(a, b float64) float64 {
	d := math.Mod(b-a, 2*math.Pi)
	if d > math.Pi {
		d -= 2 * math.Pi
	} else if d < -math.Pi {
		d += 2 * math.Pi
	}
	return d
}

// NormAngle normalizes r into [0, 2π).
func NormAngle(r float64) float64 {
	r = math.Mod(r, 2*math.Pi)
	if r < 0 {
		r += 2 * math.Pi
	}
	return r
}

// SimplifyRDP simplifies pts using the Ramer-Douglas-Peucker algorithm with tolerance tol.
func SimplifyRDP(pts []Vec, tol float64) []Vec {
	if len(pts) < 3 {
		return append([]Vec(nil), pts...)
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	var walk func(i, j int)
	walk = func(i, j int) {
		if j <= i+1 {
			return
		}
		best, at := 0.0, -1
		for k := i + 1; k < j; k++ {
			if d := DistToSegment(pts[k], pts[i], pts[j]); d > best {
				best, at = d, k
			}
		}
		if best > tol {
			keep[at] = true
			walk(i, at)
			walk(at, j)
		}
	}
	walk(0, len(pts)-1)
	out := make([]Vec, 0, len(pts)/2+2)
	for i, p := range pts {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

// PolyLength returns the total arc length of pts.
func PolyLength(pts []Vec) float64 {
	var total float64
	for i := 1; i < len(pts); i++ {
		total += pts[i-1].Dist(pts[i])
	}
	return total
}

type interval struct{ lo, hi float64 }

// segmentDiscInterval returns the parameter range [t0, t1] in [0, 1] where
// segment ab lies inside or on the disc centered at c with radius r.
func segmentDiscInterval(a, b, c Vec, r float64) (interval, bool) {
	d := b.Sub(a)
	f := a.Sub(c)
	aa := d.Dot(d)
	if aa == 0 {
		if f.LenSq() <= r*r {
			return interval{0, 1}, true
		}
		return interval{}, false
	}
	bb := 2 * f.Dot(d)
	cc := f.Dot(f) - r*r
	disc := bb*bb - 4*aa*cc
	if disc < 0 {
		return interval{}, false
	}
	sq := math.Sqrt(disc)
	t0 := (-bb - sq) / (2 * aa)
	t1 := (-bb + sq) / (2 * aa)
	if t1 < 0 || t0 > 1 {
		return interval{}, false
	}
	t0 = Clamp(t0, 0, 1)
	t1 = Clamp(t1, 0, 1)
	if t1 <= t0 {
		return interval{}, false
	}
	return interval{t0, t1}, true
}

// ClipSegmentOutsideDiscs subtracts a slice of eraser discs from segment ab and
// returns the remaining sub-segments in order along a -> b.
func ClipSegmentOutsideDiscs(a, b Vec, centers []Vec, r float64) [][2]Vec {
	if r <= 0 || len(centers) == 0 {
		return [][2]Vec{{a, b}}
	}
	var covered []interval
	for _, c := range centers {
		if iv, ok := segmentDiscInterval(a, b, c, r); ok {
			covered = append(covered, iv)
		}
	}
	if len(covered) == 0 {
		return [][2]Vec{{a, b}}
	}
	sort.Slice(covered, func(i, j int) bool {
		if covered[i].lo != covered[j].lo {
			return covered[i].lo < covered[j].lo
		}
		return covered[i].hi < covered[j].hi
	})
	merged := covered[:0]
	for _, iv := range covered {
		if len(merged) == 0 || iv.lo > merged[len(merged)-1].hi {
			merged = append(merged, iv)
		} else if iv.hi > merged[len(merged)-1].hi {
			merged[len(merged)-1].hi = iv.hi
		}
	}
	var out [][2]Vec
	cur := 0.0
	const minSpan = 1e-4
	for _, iv := range merged {
		if iv.lo-cur > minSpan {
			out = append(out, [2]Vec{a.Lerp(b, cur), a.Lerp(b, iv.lo)})
		}
		if iv.hi > cur {
			cur = iv.hi
		}
	}
	if 1-cur > minSpan {
		out = append(out, [2]Vec{a.Lerp(b, cur), b})
	}
	return out
}

// ClipPolylineOutsideCapsules clips polyline pts against an eraser path swept with
// radius r (sampled along capsules into discs spaced <= r/2), returning 0..N
// surviving continuous polylines.
func ClipPolylineOutsideCapsules(pts []Vec, eraserPath []Vec, r float64) [][]Vec {
	if len(pts) == 0 {
		return nil
	}
	if len(eraserPath) == 0 || r <= 0 {
		cp := append([]Vec(nil), pts...)
		return [][]Vec{cp}
	}
	// Densify the eraser path into overlapping discs so fast sweeps leave no gaps.
	var discs []Vec
	step := math.Max(r*0.35, 1)
	discs = append(discs, eraserPath[0])
	for i := 1; i < len(eraserPath); i++ {
		a, b := eraserPath[i-1], eraserPath[i]
		dist := a.Dist(b)
		n := int(math.Ceil(dist / step))
		if n < 1 {
			n = 1
		}
		for k := 1; k <= n; k++ {
			discs = append(discs, a.Lerp(b, float64(k)/float64(n)))
		}
	}
	if len(pts) == 1 {
		for _, c := range discs {
			if pts[0].Dist(c) <= r {
				return nil
			}
		}
		return [][]Vec{{pts[0]}}
	}
	var result [][]Vec
	var cur []Vec
	flush := func() {
		if len(cur) >= 2 {
			result = append(result, cur)
		}
		cur = nil
	}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		pieces := ClipSegmentOutsideDiscs(a, b, discs, r)
		for _, seg := range pieces {
			if len(cur) == 0 {
				cur = []Vec{seg[0], seg[1]}
			} else if cur[len(cur)-1].Dist(seg[0]) < 1e-3 {
				cur = append(cur, seg[1])
			} else {
				flush()
				cur = []Vec{seg[0], seg[1]}
			}
		}
		if len(pieces) == 0 {
			flush()
		}
	}
	flush()
	return result
}
