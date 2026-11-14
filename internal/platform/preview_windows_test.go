//go:build windows

package platform

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestPreviewExtensions(t *testing.T) {
	cases := []struct {
		path  string
		isDir bool
		want  []string
	}{
		{`C:\Users\me\notes.TXT`, false, []string{".txt", "*"}},
		{`C:\Users\me\photo.JPEG`, false, []string{".jpeg", "*"}},
		{`C:\Users\me\archive.tar.gz`, false, []string{".gz", "*"}},
		{`C:\Users\me\README`, false, []string{"*"}},
		{`C:\Users\me\Documents`, true, []string{"Directory"}},
	}
	for _, c := range cases {
		got := previewExtensions(c.path, c.isDir)
		if len(got) != len(c.want) {
			t.Fatalf("previewExtensions(%q, %v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("previewExtensions(%q, %v) = %v, want %v", c.path, c.isDir, got, c.want)
			}
		}
	}
}

func TestPreviewParseCLSID(t *testing.T) {
	want := iidPVPreviewHandler
	for _, in := range []string{
		`{8895b1c6-b41f-4c1c-a562-0d564250836f}`,
		`8895b1c6-b41f-4c1c-a562-0d564250836f`,
		` {8895B1C6-B41F-4C1C-A562-0D564250836F} `,
	} {
		got, err := previewParseCLSID(in)
		if err != nil {
			t.Fatalf("previewParseCLSID(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("previewParseCLSID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPreviewTitle(t *testing.T) {
	got := previewTitle(`C:\Users\me\report.pdf`, 1, 3)
	if want := "report.pdf — 2 / 3"; got != want {
		t.Fatalf("previewTitle = %q, want %q", got, want)
	}
}

func TestPreviewStep(t *testing.T) {
	cases := []struct{ index, delta, count, want int }{
		{0, 1, 3, 1},
		{1, 1, 3, 2},
		{2, 1, 3, 0},
		{0, -1, 3, 2},
		{1, -1, 3, 0},
		{0, 1, 0, 0},
		{0, -1, 1, 0},
	}
	for _, c := range cases {
		if got := previewStep(c.index, c.delta, c.count); got != c.want {
			t.Fatalf("previewStep(%d, %d, %d) = %d, want %d", c.index, c.delta, c.count, got, c.want)
		}
	}
}

func TestPreviewWindowRect(t *testing.T) {
	// A 900x640 window on a 1920x1040 work area (a typical 1080p desktop
	// with a taskbar) is centered with room to spare.
	got := previewWindowRect(Rect{X: 0, Y: 0, Width: 1920, Height: 1040}, 900, 640)
	want := Rect{X: 510, Y: 200, Width: 900, Height: 640}
	if got != want {
		t.Fatalf("previewWindowRect = %+v, want %+v", got, want)
	}
	// A work area smaller than the panel shrinks it to leave the margin.
	got = previewWindowRect(Rect{X: 0, Y: 0, Width: 800, Height: 500}, 900, 640)
	want = Rect{X: 40, Y: 40, Width: 720, Height: 420}
	if got != want {
		t.Fatalf("previewWindowRect on a small display = %+v, want %+v", got, want)
	}
	// An unknown work area falls back to the unplaced default size.
	got = previewWindowRect(Rect{}, 900, 640)
	want = Rect{X: 0, Y: 0, Width: 900, Height: 640}
	if got != want {
		t.Fatalf("previewWindowRect without a display = %+v, want %+v", got, want)
	}
}

// TestPreviewHandlerThroughProgID covers the registration form the explorer
// uses for most document types: the extension names a ProgID, and the ProgID
// carries the preview handler. The lookup used to stop at the extension and
// SystemFileAssociations, so those files fell back to "no preview available"
// while Explorer previewed them.
func TestPreviewHandlerThroughProgID(t *testing.T) {
	const (
		progID = "RakkoPreviewTest.Document"
		ext    = ".rakkoprevtest"
	)
	handlerKey := `Software\Classes\` + progID + `\ShellEx\` + previewHandlerClass
	progIDKey := `Software\Classes\` + progID
	extKey := `Software\Classes\` + ext
	want := windows.GUID{Data1: 0x1531d583, Data2: 0x8375, Data3: 0x4d3f, Data4: [8]byte{0xb5, 0xfb, 0xd2, 0x3b, 0xbd, 0x16, 0x9f, 0x22}}

	remove := func() {
		registry.DeleteKey(registry.CURRENT_USER, handlerKey)
		registry.DeleteKey(registry.CURRENT_USER, progIDKey)
		registry.DeleteKey(registry.CURRENT_USER, extKey)
	}
	remove()
	t.Cleanup(remove)

	handler, _, err := registry.CreateKey(registry.CURRENT_USER, handlerKey, registry.WRITE)
	if err != nil {
		t.Skipf("cannot write a test association: %v", err)
	}
	if err := handler.SetStringValue("", want.String()); err != nil {
		handler.Close()
		t.Fatalf("writing the handler value: %v", err)
	}
	handler.Close()
	prog, _, err := registry.CreateKey(registry.CURRENT_USER, progIDKey, registry.WRITE)
	if err != nil {
		t.Fatalf("creating the ProgID: %v", err)
	}
	prog.Close()
	extension, _, err := registry.CreateKey(registry.CURRENT_USER, extKey, registry.WRITE)
	if err != nil {
		t.Fatalf("creating the extension: %v", err)
	}
	if err := extension.SetStringValue("", progID); err != nil {
		extension.Close()
		t.Fatalf("naming the ProgID: %v", err)
	}
	extension.Close()

	if got := previewProgID(ext); !strings.EqualFold(got, progID) {
		t.Fatalf("previewProgID(%q) = %q, want %q", ext, got, progID)
	}
	got, ok := previewHandlerCLSID(`C:\work\report`+ext, false)
	if !ok {
		t.Fatalf("previewHandlerCLSID did not find the handler registered on %s", progID)
	}
	if got != want {
		t.Fatalf("previewHandlerCLSID = %s, want %s", got, want)
	}
}
