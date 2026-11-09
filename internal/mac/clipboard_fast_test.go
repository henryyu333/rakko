//go:build darwin

package mac

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestClipboardChangeCountFastHasPool runs the worker-thread read in a child
// process with OBJC_DEBUG_MISSING_POOLS=YES: a missing autorelease pool
// prints "autoreleased with no pool in place - just leaking" per call.
func TestClipboardChangeCountFastHasPool(t *testing.T) {
	if os.Getenv("RK_CLIP_FAST_CHILD") == "1" {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20000 {
				ClipboardChangeCountFast()
			}
		}()
		wg.Wait()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestClipboardChangeCountFastHasPool")
	cmd.Env = append(os.Environ(), "RK_CLIP_FAST_CHILD=1", "OBJC_DEBUG_MISSING_POOLS=YES")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "MISSING POOLS") {
		t.Fatalf("background changeCount leaked an autoreleased object:\n%s", out)
	}
}
