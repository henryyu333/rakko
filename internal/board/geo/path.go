package geo

import (
	"fmt"
	"math"
	"strings"
)

// Op is a vector path command.
type Op uint8

const (
	OpMove Op = iota
	OpLine
	OpQuad
	OpCube
	OpClose
)

// Seg is a single segment of a Path.
// P holds 1 point for Move/Line, control+end for Quad, two controls+end for Cube.
type Seg struct {
	Op Op
	P  [3]Vec
}

// Path is a renderer-independent 2D vector path shared by screen rendering and SVG export.
type Path struct {
	Segs []Seg
}

// Move starts a new subpath at v.
func (p *Path) Move(v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpMove, P: [3]Vec{v}})
	return p
}

// Line adds a straight segment to v.
func (p *Path) Line(v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpLine, P: [3]Vec{v}})
	return p
}

// Quad adds a quadratic Bezier curve to v with control point c.
func (p *Path) Quad(c, v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpQuad, P: [3]Vec{c, v}})
	return p
}

// Cube adds a cubic Bezier curve to v with control points c1 and c2.
func (p *Path) Cube(c1, c2, v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpCube, P: [3]Vec{c1, c2, v}})
	return p
}

// Close closes the current subpath.
func (p *Path) Close() *Path {
	p.Segs = append(p.Segs, Seg{Op: OpClose})
	return p
}

// Poly appends a polyline through pts, closing it if closed is true.
func (p *Path) Poly(pts []Vec, closed bool) *Path {
	for i, v := range pts {
		if i == 0 {
			p.Move(v)
		} else {
			p.Line(v)
		}
	}
	if closed && len(pts) > 0 {
		p.Close()
	}
	return p
}

// Smooth appends a quadratic midpoint-interpolated curve through pts.
func (p *Path) Smooth(pts []Vec, closed bool) *Path {
	n := len(pts)
	switch {
	case n == 0:
		return p
	case n < 3:
		return p.Poly(pts, closed)
	}
	if closed {
		p.Move(Med(pts[n-1], pts[0]))
		for i := 0; i < n; i++ {
			p.Quad(pts[i], Med(pts[i], pts[(i+1)%n]))
		}
		return p.Close()
	}
	p.Move(pts[0])
	for i := 1; i < n-1; i++ {
		p.Quad(pts[i], Med(pts[i], pts[i+1]))
	}
	return p.Line(pts[n-1])
}

// EllipsePath returns a closed 4-cubic-Bezier ellipse inscribed in b.
func EllipsePath(b Box) *Path {
	const k = 0.5522847498307936
	c := b.Center()
	rx, ry := b.W/2, b.H/2
	p := &Path{}
	p.Move(V(c.X+rx, c.Y))
	p.Cube(V(c.X+rx, c.Y+k*ry), V(c.X+k*rx, c.Y+ry), V(c.X, c.Y+ry))
	p.Cube(V(c.X-k*rx, c.Y+ry), V(c.X-rx, c.Y+k*ry), V(c.X-rx, c.Y))
	p.Cube(V(c.X-rx, c.Y-k*ry), V(c.X-k*rx, c.Y-ry), V(c.X, c.Y-ry))
	p.Cube(V(c.X+k*rx, c.Y-ry), V(c.X+rx, c.Y-k*ry), V(c.X+rx, c.Y))
	return p.Close()
}

// RoundedRectPath returns a closed rectangle path for b with corner radius r.
func RoundedRectPath(b Box, r float64) *Path {
	corners := b.Corners()
	if r <= 0 {
		return (&Path{}).Poly(corners[:], true)
	}
	r = math.Min(r, math.Min(b.W, b.H)/2)
	return RoundedPoly(corners[:], r)
}

// RoundedPoly returns a closed polygon path with corners rounded up to radius r.
func RoundedPoly(pts []Vec, r float64) *Path {
	n := len(pts)
	p := &Path{}
	if n < 3 {
		return p.Poly(pts, true)
	}
	for i := 0; i < n; i++ {
		prev, cur, next := pts[(i+n-1)%n], pts[i], pts[(i+1)%n]
		d := math.Min(r, math.Min(cur.Dist(prev), cur.Dist(next))/2)
		a := cur.Add(prev.Sub(cur).Unit().Mul(d))
		b := cur.Add(next.Sub(cur).Unit().Mul(d))
		if i == 0 {
			p.Move(a)
		} else {
			p.Line(a)
		}
		p.Quad(cur, b)
	}
	return p.Close()
}

// Flatten tessellates each curve command into n segments and returns the resulting polylines.
func (p *Path) Flatten(n int) (subs [][]Vec, closed []bool) {
	if n < 1 {
		n = 1
	}
	var cur []Vec
	flush := func(c bool) {
		if len(cur) > 0 {
			subs = append(subs, cur)
			closed = append(closed, c)
		}
		cur = nil
	}
	for _, s := range p.Segs {
		switch s.Op {
		case OpMove:
			flush(false)
			cur = append(cur, s.P[0])
		case OpLine:
			cur = append(cur, s.P[0])
		case OpQuad:
			if len(cur) == 0 {
				continue
			}
			from := cur[len(cur)-1]
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				pt := from.Mul(u*u).Add(s.P[0].Mul(2*u*t)).Add(s.P[1].Mul(t*t))
				cur = append(cur, pt)
			}
		case OpCube:
			if len(cur) == 0 {
				continue
			}
			from := cur[len(cur)-1]
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				pt := from.Mul(u*u*u).
					Add(s.P[0].Mul(3*u*u*t)).
					Add(s.P[1].Mul(3*u*t*t)).
					Add(s.P[2].Mul(t*t*t))
				cur = append(cur, pt)
			}
		case OpClose:
			flush(true)
		}
	}
	flush(false)
	return subs, closed
}

// Points returns the first flattened subpath of p and whether it is closed.
func (p *Path) Points(n int) ([]Vec, bool) {
	subs, closed := p.Flatten(n)
	if len(subs) == 0 {
		return nil, false
	}
	return subs[0], closed[0]
}

// SVG encodes p as an SVG path "d" attribute after applying matrix m.
func (p *Path) SVG(m Mat) string {
	var b strings.Builder
	writePt := func(v Vec) {
		v = m.Apply(v)
		fmt.Fprintf(&b, "%.2f %.2f ", v.X, v.Y)
	}
	for _, s := range p.Segs {
		switch s.Op {
		case OpMove:
			b.WriteByte('M')
			writePt(s.P[0])
		case OpLine:
			b.WriteByte('L')
			writePt(s.P[0])
		case OpQuad:
			b.WriteByte('Q')
			writePt(s.P[0])
			writePt(s.P[1])
		case OpCube:
			b.WriteByte('C')
			writePt(s.P[0])
			writePt(s.P[1])
			writePt(s.P[2])
		case OpClose:
			b.WriteByte('Z')
		}
	}
	return strings.TrimSpace(b.String())
}
