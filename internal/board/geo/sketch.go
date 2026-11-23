package geo

import "math"

// Hash computes a deterministic 32-bit FNV-1a hash from s.
func Hash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// Rng is a deterministic xorshift32 pseudo-random generator.
type Rng uint32

// NewRng creates a deterministic generator seeded by s.
func NewRng(s string) *Rng {
	r := Rng(Hash(s) | 1)
	return &r
}

// Next returns a deterministic float64 in [-1, 1).
func (r *Rng) Next() float64 {
	x := uint32(*r)
	if x == 0 {
		x = 1
	}
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*r = Rng(x)
	return float64(x)/float64(1<<31) - 1
}

// StrokePoint is a freehand input sample with normalized pressure Z in [0, 1].
type StrokePoint struct {
	X, Y, Z float64
}

// RibbonOutline builds a closed variable-width polygon outline for a freehand
// stroke with start/end taper and pressure-modulated half-widths.
func RibbonOutline(pts []StrokePoint, baseWidth float64, simulatePressure bool) []Vec {
	n := len(pts)
	if n == 0 || baseWidth <= 0 {
		return nil
	}
	if n == 1 {
		r := math.Max(baseWidth*0.4, 0.5)
		out := make([]Vec, 12)
		for i := range out {
			a := float64(i) * 2 * math.Pi / float64(len(out))
			out[i] = V(pts[0].X+math.Cos(a)*r, pts[0].Y+math.Sin(a)*r)
		}
		return out
	}
	cumLen := make([]float64, n)
	for i := 1; i < n; i++ {
		cumLen[i] = cumLen[i-1] + math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
	}
	totalLen := cumLen[n-1]
	taperLen := math.Min(baseWidth*3.5, totalLen*0.35)

	left := make([]Vec, n)
	right := make([]Vec, n)
	prevHalf := baseWidth * 0.5
	for i := 0; i < n; i++ {
		var dir Vec
		switch {
		case i == 0:
			dir = V(pts[1].X-pts[0].X, pts[1].Y-pts[0].Y)
		case i == n-1:
			dir = V(pts[n-1].X-pts[n-2].X, pts[n-1].Y-pts[n-2].Y)
		default:
			dir = V(pts[i+1].X-pts[i-1].X, pts[i+1].Y-pts[i-1].Y)
		}
		u := dir.Unit()
		if u.LenSq() == 0 {
			u = V(1, 0)
		}
		perp := u.Perp()

		p := pts[i].Z
		if simulatePressure && i > 0 {
			dist := math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
			// Fast strokes thin slightly; slow strokes breathe wider.
			sp := Clamp(1.0-dist/(baseWidth*8.0+12.0), 0.35, 1.0)
			p = sp
		} else if p <= 0 {
			p = 0.5
		}
		w := baseWidth * (0.35 + 0.85*Clamp(p, 0, 1))
		if taperLen > 0 {
			startT := Clamp(cumLen[i]/taperLen, 0.25, 1)
			endT := Clamp((totalLen-cumLen[i])/taperLen, 0.25, 1)
			w *= math.Min(startT, endT)
		}
		half := prevHalf*0.45 + (w*0.5)*0.55
		prevHalf = half
		c := V(pts[i].X, pts[i].Y)
		left[i] = c.Add(perp.Mul(half))
		right[i] = c.Sub(perp.Mul(half))
	}
	out := make([]Vec, 0, 2*n)
	out = append(out, left...)
	for i := n - 1; i >= 0; i-- {
		out = append(out, right[i])
	}
	return out
}
