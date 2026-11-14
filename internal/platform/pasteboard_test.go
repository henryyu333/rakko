//go:build darwin

package platform

import (
	"slices"
	"testing"
)

// 用独立的临时剪贴板验证类型读取，绝不触碰用户的系统剪贴板。
func TestPasteboardTypesSeesConcealedMarker(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	var types []string
	withPool(func() {
		board := send(class("NSPasteboard"), "pasteboardWithUniqueName")
		defer send(board, "releaseGlobally")
		send(board, "clearContents")
		send(board, "setString:forType:", uintptr(nsString("secret")), uintptr(nsString("public.utf8-plain-text")))
		send(board, "setString:forType:", uintptr(nsString("")), uintptr(nsString("org.nspasteboard.ConcealedType")))
		types = pasteboardTypes(board)
	})
	for _, want := range []string{"public.utf8-plain-text", "org.nspasteboard.ConcealedType"} {
		if !slices.Contains(types, want) {
			t.Fatalf("pasteboardTypes = %q, missing %q", types, want)
		}
	}
}
