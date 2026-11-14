//go:build windows

package main

// The capsule's return zone is 41pt tall plus its hover margin; a 96pt
// otter parked at the work area's top edge has its centre half its height
// below that band, so on Windows the pointer the drag carried to the
// capsule decides whether a release is a return. The release and the
// 松开收进刘海 pill read the same answer.

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestPetReturnAcceptsThePointerOnWindows(t *testing.T) {
	a := &shelf{}
	a.island = true
	capsule := a.islandRect()
	release := mygo.Point{X: capsule.X + capsule.Width/2, Y: capsule.Y + 20}
	centre := mygo.Point{X: release.X + 37, Y: release.Y + 48}
	if a.pinReturnZone(centre) {
		t.Fatalf("the otter's centre %v already reaches the capsule %+v; the pointer is not what decides", centre, capsule)
	}
	if !petReturnTarget(a, centre, release) {
		t.Fatalf("a release at %v on the capsule %+v did not count as a return", release, capsule)
	}
	if petReturnTarget(a, centre, mygo.Point{X: 400, Y: 700}) {
		t.Fatal("a release away from the capsule counted as a return")
	}
}
