//go:build !windows

package main

import "github.com/egoist/mygo"

func platformUpperSegment(ds []mygo.Display, target mygo.Display, segX, segY, segW int) (bx, by, bw int, ok bool) {
	return upperDisplayTouchesSegment(ds, target, segX, segY, segW)
}
