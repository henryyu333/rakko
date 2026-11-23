// Package geo implements zero-dependency 2D plane geometry for the whiteboard:
// vectors, axis-aligned boxes, affine matrices, vector paths, intersection tests,
// partial-eraser clipping, and deterministic hand-drawn stroke ribbons.
package geo

import "math"

// Vec is a 2D point or vector.
type Vec struct {
	X, Y float64
}

// V returns the vector (x, y).
func V(x, y float64) Vec { return Vec{X: x, Y: y} }

// Add returns a + b.
func (a Vec) Add(b Vec) Vec { return Vec{X: a.X + b.X, Y: a.Y + b.Y} }

// Sub returns a - b.
func (a Vec) Sub(b Vec) Vec { return Vec{X: a.X - b.X, Y: a.Y - b.Y} }

// Mul returns a scaled by k.
func (a Vec) Mul(k float64) Vec { return Vec{X: a.X * k, Y: a.Y * k} }

// Div returns a divided by k, or zero if k == 0.
func (a Vec) Div(k float64) Vec {
	if k == 0 {
		return Vec{}
	}
	return Vec{X: a.X / k, Y: a.Y / k}
}

// Dot returns the dot product a · b.
func (a Vec) Dot(b Vec) float64 { return a.X*b.X + a.Y*b.Y }

// Cross returns the 2D cross product z-component a × b.
func (a Vec) Cross(b Vec) float64 { return a.X*b.Y - a.Y*b.X }

// LenSq returns the squared Euclidean length of a.
func (a Vec) LenSq() float64 { return a.X*a.X + a.Y*a.Y }

// Len returns the Euclidean length of a.
func (a Vec) Len() float64 { return math.Hypot(a.X, a.Y) }

// Dist returns the distance between a and b.
func (a Vec) Dist(b Vec) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// Unit returns a normalized to length 1, or the zero vector if a has length 0.
func (a Vec) Unit() Vec {
	l := a.Len()
	if l == 0 {
		return Vec{}
	}
	return Vec{X: a.X / l, Y: a.Y / l}
}

// Perp returns a rotated 90 degrees counter-clockwise in screen coordinates (y down: (y, -x)).
func (a Vec) Perp() Vec { return Vec{X: a.Y, Y: -a.X} }

// Rot returns a rotated by r radians.
func (a Vec) Rot(r float64) Vec {
	s, c := math.Sincos(r)
	return Vec{X: a.X*c - a.Y*s, Y: a.X*s + a.Y*c}
}

// RotAround returns a rotated by r radians around origin o.
func (a Vec) RotAround(o Vec, r float64) Vec { return a.Sub(o).Rot(r).Add(o) }

// Lerp linearly interpolates between a and b at parameter t.
func (a Vec) Lerp(b Vec, t float64) Vec {
	return Vec{X: a.X + (b.X-a.X)*t, Y: a.Y + (b.Y-a.Y)*t}
}

// Angle returns the angle from a to b in radians.
func (a Vec) Angle(b Vec) float64 { return math.Atan2(b.Y-a.Y, b.X-a.X) }

// Med returns the midpoint between a and b.
func Med(a, b Vec) Vec { return Vec{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2} }

// Box is an axis-aligned rectangle.
type Box struct {
	X, Y, W, H float64
}

// MaxX returns the right edge of b.
func (b Box) MaxX() float64 { return b.X + b.W }

// MaxY returns the bottom edge of b.
func (b Box) MaxY() float64 { return b.Y + b.H }

// Center returns the center point of b.
func (b Box) Center() Vec { return Vec{X: b.X + b.W/2, Y: b.Y + b.H/2} }

// Corners returns the four corners of b clockwise from top-left.
func (b Box) Corners() [4]Vec {
	return [4]Vec{
		{X: b.X, Y: b.Y},
		{X: b.X + b.W, Y: b.Y},
		{X: b.X + b.W, Y: b.Y + b.H},
		{X: b.X, Y: b.Y + b.H},
	}
}

// Contains reports whether p lies inside or on the boundary of b.
func (b Box) Contains(p Vec) bool {
	return p.X >= b.X && p.X <= b.X+b.W && p.Y >= b.Y && p.Y <= b.Y+b.H
}

// ContainsBox reports whether o is completely inside b.
func (b Box) ContainsBox(o Box) bool {
	return o.X >= b.X && o.Y >= b.Y && o.MaxX() <= b.MaxX() && o.MaxY() <= b.MaxY()
}

// Overlaps reports whether b and o intersect (including touching edges).
func (b Box) Overlaps(o Box) bool {
	return b.X <= o.MaxX() && o.X <= b.MaxX() && b.Y <= o.MaxY() && o.Y <= b.MaxY()
}

// Union returns the smallest box enclosing both b and o.
func (b Box) Union(o Box) Box {
	x0 := math.Min(b.X, o.X)
	y0 := math.Min(b.Y, o.Y)
	x1 := math.Max(b.MaxX(), o.MaxX())
	y1 := math.Max(b.MaxY(), o.MaxY())
	return Box{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// Expand grows b by d on all four sides.
func (b Box) Expand(d float64) Box {
	return Box{X: b.X - d, Y: b.Y - d, W: b.W + 2*d, H: b.H + 2*d}
}

// BoxFrom constructs a normalized box from two opposite corners in any order.
func BoxFrom(a, b Vec) Box {
	return Box{
		X: math.Min(a.X, b.X),
		Y: math.Min(a.Y, b.Y),
		W: math.Abs(a.X - b.X),
		H: math.Abs(a.Y - b.Y),
	}
}

// BoxOf returns the bounding box of pts, or a zero box if pts is empty.
func BoxOf(pts []Vec) Box {
	if len(pts) == 0 {
		return Box{}
	}
	x0, y0, x1, y1 := pts[0].X, pts[0].Y, pts[0].X, pts[0].Y
	for _, p := range pts[1:] {
		x0 = math.Min(x0, p.X)
		y0 = math.Min(y0, p.Y)
		x1 = math.Max(x1, p.X)
		y1 = math.Max(y1, p.Y)
	}
	return Box{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// Clamp restricts v to [lo, hi].
func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Mat is a 2D affine transform:
//
//	x' = A*x + C*y + E
//	y' = B*x + D*y + F
type Mat struct {
	A, B, C, D, E, F float64
}

// Identity is the identity affine matrix.
var Identity = Mat{A: 1, D: 1}

// Translate returns a translation matrix by (x, y).
func Translate(x, y float64) Mat { return Mat{A: 1, D: 1, E: x, F: y} }

// Rotate returns a rotation matrix of r radians around the origin.
func Rotate(r float64) Mat {
	s, c := math.Sincos(r)
	return Mat{A: c, B: s, C: -s, D: c}
}

// Scale returns a scaling matrix by (x, y).
func Scale(x, y float64) Mat { return Mat{A: x, D: y} }

// Compose returns a rigid transform translating to (x, y) and rotating by r radians.
func Compose(x, y, r float64) Mat {
	s, c := math.Sincos(r)
	return Mat{A: c, B: s, C: -s, D: c, E: x, F: y}
}

// Mul returns m * o (applies o first, then m).
func (m Mat) Mul(o Mat) Mat {
	return Mat{
		A: m.A*o.A + m.C*o.B,
		B: m.B*o.A + m.D*o.B,
		C: m.A*o.C + m.C*o.D,
		D: m.B*o.C + m.D*o.D,
		E: m.A*o.E + m.C*o.F + m.E,
		F: m.B*o.E + m.D*o.F + m.F,
	}
}

// Apply transforms point p.
func (m Mat) Apply(p Vec) Vec {
	return Vec{X: m.A*p.X + m.C*p.Y + m.E, Y: m.B*p.X + m.D*p.Y + m.F}
}

// ApplyDir transforms direction vector p without translation.
func (m Mat) ApplyDir(p Vec) Vec {
	return Vec{X: m.A*p.X + m.C*p.Y, Y: m.B*p.X + m.D*p.Y}
}

// Invert returns the inverse of m, or Identity if m is singular.
func (m Mat) Invert() Mat {
	det := m.A*m.D - m.B*m.C
	if det == 0 {
		return Identity
	}
	return Mat{
		A: m.D / det,
		B: -m.B / det,
		C: -m.C / det,
		D: m.A / det,
		E: (m.C*m.F - m.D*m.E) / det,
		F: (m.B*m.E - m.A*m.F) / det,
	}
}

// Rotation returns the rotation angle encoded in m, in radians.
func (m Mat) Rotation() float64 { return math.Atan2(m.B, m.A) }

// ApplyBox transforms the four corners of b.
func (m Mat) ApplyBox(b Box) [4]Vec {
	cs := b.Corners()
	for i := range cs {
		cs[i] = m.Apply(cs[i])
	}
	return cs
}

// BoundsOf returns the axis-aligned bounding box of b after applying m.
func (m Mat) BoundsOf(b Box) Box {
	cs := m.ApplyBox(b)
	return BoxOf(cs[:])
}
