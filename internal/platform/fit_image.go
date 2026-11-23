package platform

// fitQuality is the lowest quality 「压缩到」tries before shrinking pixels.
const fitQuality = 0.4

// fitMinPx is the smallest longer side a fitted image shrinks to.
const fitMinPx = 640

// fitImageData drives FitImage's shared quality binary-search and 0.85x
// downscale loop while keeping platform decode/scale/encode in the callbacks.
func fitImageData(
	origPx int,
	lossy bool,
	limit int64,
	makeFrame func(maxPx int) (frame any, release func(), err error),
	encode func(frame any, quality float64) ([]byte, error),
) (best []byte, err error) {
	maxPx := origPx
	var smallest []byte
	for {
		frame, release, e := makeFrame(maxPx)
		if e != nil {
			return nil, e
		}
		fit := false
		if lossy {
			lo, hi := fitQuality, 1.0
			for range 7 {
				q := (lo + hi) / 2
				data, e := encode(frame, q)
				if e != nil {
					if release != nil {
						release()
					}
					return nil, e
				}
				if len(smallest) == 0 || len(data) < len(smallest) {
					smallest = data
				}
				if int64(len(data)) <= limit {
					best, fit = data, true
					lo = q
				} else {
					hi = q
				}
				if hi-lo < 0.02 {
					break
				}
			}
		} else {
			data, e := encode(frame, 0)
			if e != nil {
				if release != nil {
					release()
				}
				return nil, e
			}
			if len(smallest) == 0 || len(data) < len(smallest) {
				smallest = data
			}
			fit = int64(len(data)) <= limit
			best = data
		}
		if release != nil {
			release()
		}
		if fit || maxPx <= fitMinPx {
			break
		}
		next := int(float64(maxPx) * 0.85)
		if next < fitMinPx {
			next = fitMinPx
		}
		if next >= maxPx {
			break
		}
		maxPx = next
	}
	if len(best) == 0 {
		best = smallest
	}
	return best, nil
}
