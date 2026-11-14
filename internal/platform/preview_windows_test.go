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
	shellExKey := `Software\Classes\` + progID + `\ShellEx`
	progIDKey := `Software\Classes\` + progID
	extKey := `Software\Classes\` + ext
	want := windows.GUID{Data1: 0x1531d583, Data2: 0x8375, Data3: 0x4d3f, Data4: [8]byte{0xb5, 0xfb, 0xd2, 0x3b, 0xbd, 0x16, 0x9f, 0x22}}

	// DeleteKey only removes an empty key, so the deepest key goes first and
	// the intermediate ShellEx key is named explicitly.
	remove := func() {
		registry.DeleteKey(registry.CURRENT_USER, handlerKey)
		registry.DeleteKey(registry.CURRENT_USER, shellExKey)
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

// TestPreviewActivationContext checks that a handler registered to run in the
// shell's DLL surrogate is activated there, while a plain in-process handler
// keeps the combined context. Explorer honours this registration; ignoring it
// loads the handler's DLL into Rakko, where the PDF preview handler fails.
func TestPreviewActivationContext(t *testing.T) {
	const (
		clsidKey = `Software\Classes\CLSID\{0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d}`
		appID    = "RakkoPreviewTest.Surrogate"
		appIDKey = `Software\Classes\AppID\` + appID
		plainKey = `Software\Classes\CLSID\{0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4e}`
	)
	clsid := windows.GUID{Data1: 0x0a1b2c3d, Data2: 0x4e5f, Data3: 0x4a6b, Data4: [8]byte{0x8c, 0x7d, 0x9e, 0x0f, 0x1a, 0x2b, 0x3c, 0x4d}}
	plain := clsid
	plain.Data4[7] = 0x4e

	remove := func() {
		registry.DeleteKey(registry.CURRENT_USER, clsidKey)
		registry.DeleteKey(registry.CURRENT_USER, appIDKey)
		registry.DeleteKey(registry.CURRENT_USER, plainKey)
	}
	remove()
	t.Cleanup(remove)

	k, _, err := registry.CreateKey(registry.CURRENT_USER, clsidKey, registry.WRITE)
	if err != nil {
		t.Skipf("cannot write a test class: %v", err)
	}
	if err := k.SetStringValue("AppID", appID); err != nil {
		k.Close()
		t.Fatalf("writing the AppID value: %v", err)
	}
	k.Close()
	a, _, err := registry.CreateKey(registry.CURRENT_USER, appIDKey, registry.WRITE)
	if err != nil {
		t.Fatalf("creating the AppID: %v", err)
	}
	if err := a.SetStringValue("DllSurrogate", `%SystemRoot%\system32\prevhost.exe`); err != nil {
		a.Close()
		t.Fatalf("writing the surrogate: %v", err)
	}
	a.Close()

	if got := previewActivationContext(clsid); got != pvClsctxSurrogate {
		t.Fatalf("a surrogate handler activated with %#x, want CLSCTX_LOCAL_SERVER", got)
	}
	if got := previewActivationContext(plain); got != pvClsctx {
		t.Fatalf("a plain handler activated with %#x, want the combined context", got)
	}
}
