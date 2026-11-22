//go:build windows

package main

import (
	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

func platformUpperSegment(_ []mygo.Display, _ mygo.Display, segX, segY, segW int) (bx, by, bw int, ok bool) {
	return platform.PhysicalUpperTouchesSegment(segX, segY, segW)
}
