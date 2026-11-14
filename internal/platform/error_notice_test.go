package platform

import (
	"errors"
	"testing"
)

// TestErrorNoticeInstall checks the seam later work reports through: the
// installed notice receives the error, a replacement takes over, and an
// absent notice makes the report a no-op.
func TestErrorNoticeInstall(t *testing.T) {
	t.Cleanup(func() { InstallErrorNotice(nil) })

	var got []string
	InstallErrorNotice(func(err error) { got = append(got, err.Error()) })
	reportError(errors.New("first"))
	InstallErrorNotice(func(err error) { got = append(got, "second:"+err.Error()) })
	reportError(errors.New("problem"))
	reportError(nil)

	want := []string{"first", "second:problem"}
	if len(got) != len(want) {
		t.Fatalf("notice calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notice calls = %v, want %v", got, want)
		}
	}

	InstallErrorNotice(nil)
	reportError(errors.New("dropped"))
}
