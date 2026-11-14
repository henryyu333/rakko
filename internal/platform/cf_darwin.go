//go:build darwin

package platform

import (
	"sync"

	"github.com/ebitengine/purego"
)

// releaseCF drops one CoreFoundation reference from the power-source and
// audio-device readers. Their loaders run without the drag guard, so the
// symbol is resolved here once instead of borrowing that loader's var.
var (
	releaseCFPtr func(uintptr)
	releaseCFOn  sync.Once
)

func releaseCF(obj uintptr) {
	if obj == 0 {
		return
	}
	releaseCFOn.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err == nil {
			purego.RegisterLibFunc(&releaseCFPtr, lib, "CFRelease")
		}
	})
	if releaseCFPtr != nil {
		releaseCFPtr(obj)
	}
}
