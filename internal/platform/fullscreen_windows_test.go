//go:build windows

package platform

import "testing"

func TestIsFullscreenState(t *testing.T) {
	mon := winRect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
	full := winRect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
	maximized := winRect{Left: -8, Top: -8, Right: 1928, Bottom: 1048} // taskbar leaves 40px at bottom

	cases := []struct {
		name     string
		quns     uint32
		fgOwn    bool
		fgClass  string
		fgRect   winRect
		monRect  winRect
		hasRects bool
		want     bool
	}{
		{"own window never suppresses", qunsBusy, true, "MyGoWindow", full, mon, true, false},
		{"QUNS_BUSY suppresses", qunsBusy, false, "GameWindow", maximized, mon, true, true},
		{"QUNS_RUNNING_D3D_FULL_SCREEN suppresses", qunsRunningD3DFullScreen, false, "", winRect{}, winRect{}, false, true},
		{"QUNS_PRESENTATION_MODE suppresses", qunsPresentationMode, false, "", winRect{}, winRect{}, false, true},
		{"QUNS_ACCEPTS_NOTIFICATIONS normal window", 5, false, "Notepad", maximized, mon, true, false},
		{"borderless fullscreen foreign window", 5, false, "UnityWndClass", full, mon, true, true},
		{"Progman desktop excluded", 5, false, "Progman", full, mon, true, false},
		{"WorkerW desktop excluded", 5, false, "WorkerW", full, mon, true, false},
		{"Shell_TrayWnd taskbar excluded", 5, false, "Shell_TrayWnd", full, mon, true, false},
		{"Shell_SecondaryTrayWnd excluded", 5, false, "Shell_SecondaryTrayWnd", full, mon, true, false},
		{"no rects and normal QUNS", 5, false, "GameWindow", full, mon, false, false},
		{"secondary monitor above primary", 5, false, "SDL_app", winRect{Left: 0, Top: -1080, Right: 1920, Bottom: 0}, winRect{Left: 0, Top: -1080, Right: 1920, Bottom: 0}, true, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isFullscreenState(c.quns, c.fgOwn, c.fgClass, c.fgRect, c.monRect, c.hasRects); got != c.want {
				t.Fatalf("isFullscreenState = %v, want %v", got, c.want)
			}
		})
	}
}
