//go:build darwin

package mac

import (
	"slices"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

// 服务请求的分流规则是纯逻辑：有可用文件路径时只收文件，否则收文字；
// 两个都是空时什么都不收。
func TestServiceContent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		paths     []string
		text      string
		wantPaths []string
		wantText  string
	}{
		{"files beat text", []string{"/tmp/a.txt"}, "hello", []string{"/tmp/a.txt"}, ""},
		{"only files", []string{"/a", "/b"}, "", []string{"/a", "/b"}, ""},
		{"empty paths fall to text", []string{"", ""}, "hello", nil, "hello"},
		{"only text", nil, "hello", nil, "hello"},
		{"nothing", nil, "", nil, ""},
		{"empty strings", []string{""}, "", nil, ""},
	} {
		paths, text := serviceContent(tc.paths, tc.text)
		if !slices.Equal(paths, tc.wantPaths) || text != tc.wantText {
			t.Errorf("%s: serviceContent(%q, %q) = (%q, %q), want (%q, %q)",
				tc.name, tc.paths, tc.text, paths, text, tc.wantPaths, tc.wantText)
		}
	}
}

// servicesProviderInstance registers the class once per test process and
// returns a live instance, so the tests exercise the IMP through the real
// ObjC dispatch (performSelector: equivalent), like the system does.
func servicesProviderInstance(t *testing.T) id {
	t.Helper()
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if objc.GetClass("ShelfServicesProvider") == 0 {
		if _, err := objc.RegisterClass("ShelfServicesProvider", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
			{Cmd: sel("addToShelf:userData:error:"), Fn: addToShelf},
		}); err != nil {
			t.Fatalf("register provider class: %v", err)
		}
	}
	inst := send(id(objc.GetClass("ShelfServicesProvider")), "new")
	if inst == 0 {
		t.Fatal("cannot allocate provider")
	}
	return inst
}

// 空负载要写回一个 NSString 错误消息（不是 NSError），且该指针必须在
// 回调的 autorelease 池排空后仍然有效。errOut 为 NULL 时不许崩溃。
func TestAddToShelfEmptyPayload(t *testing.T) {
	inst := servicesProviderInstance(t)
	// NULL error pointer: the selector must tolerate it.
	withPool(func() {
		board := send(class("NSPasteboard"), "pasteboardWithUniqueName")
		defer send(board, "releaseGlobally")
		send(board, "clearContents")
		send(inst, "addToShelf:userData:error:", uintptr(board), 0, 0)
	})
	// Real errOut: stores an NSString whose text survives the IMP's pool.
	var errStr id
	withPool(func() {
		board := send(class("NSPasteboard"), "pasteboardWithUniqueName")
		defer send(board, "releaseGlobally")
		send(board, "clearContents")
		send(inst, "addToShelf:userData:error:", uintptr(board), 0,
			uintptr(unsafe.Pointer(&errStr)))
	})
	if errStr == 0 {
		t.Fatal("empty payload must store an error message")
	}
	if got := goString(errStr); got != "Nothing to add to Rakko" {
		t.Fatalf("error message = %q", got)
	}
}

// 正常负载走回调：文件 URL 要转成路径，纯文字要转成 text。
func TestAddToShelfPayloads(t *testing.T) {
	inst := servicesProviderInstance(t)
	old := servicesCallback
	defer func() { servicesCallback = old }()
	var gotPaths []string
	var gotText string
	servicesCallback = func(p []string, s string) { gotPaths, gotText = p, s }

	withPool(func() {
		board := send(class("NSPasteboard"), "pasteboardWithUniqueName")
		defer send(board, "releaseGlobally")
		send(board, "clearContents")
		url := send(class("NSURL"), "fileURLWithPath:", uintptr(nsString("/tmp/shelf-svc-imp-test")))
		arr := send(class("NSArray"), "arrayWithObject:", uintptr(url))
		send(board, "writeObjects:", uintptr(arr))
		send(inst, "addToShelf:userData:error:", uintptr(board), 0, 0)
	})
	if !slices.Equal(gotPaths, []string{"/tmp/shelf-svc-imp-test"}) || gotText != "" {
		t.Fatalf("file payload delivered (%q, %q)", gotPaths, gotText)
	}

	gotPaths, gotText = nil, ""
	withPool(func() {
		board := send(class("NSPasteboard"), "pasteboardWithUniqueName")
		defer send(board, "releaseGlobally")
		send(board, "clearContents")
		send(board, "setString:forType:", uintptr(nsString("服务测试")),
			uintptr(nsString("public.utf8-plain-text")))
		send(inst, "addToShelf:userData:error:", uintptr(board), 0, 0)
	})
	if len(gotPaths) != 0 || gotText != "服务测试" {
		t.Fatalf("text payload delivered (%q, %q)", gotPaths, gotText)
	}
}
