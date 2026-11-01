//go:build darwin

package main

import (
	"testing"
	"time"
)

// TestToastsOnTheSearchBar: the bar has no toast strip. A confirmation is
// dropped (no pill to see, none to replay on a page) and a failure goes to
// Notification Center; a page keeps its bottom pill as before.
func TestToastsOnTheSearchBar(t *testing.T) {
	var calls int
	var title, body string
	old := sendNotification
	sendNotification = func(t, b string) bool { calls++; title, body = t, b; return true }
	t.Cleanup(func() { sendNotification = old })

	a, _ := levelShelf(t)
	a.visible, a.level = true, 1
	a.toastStatus("已复制", time.Time{}, statusOK)
	if calls != 0 || a.status != "" {
		t.Fatalf("a confirmation on the bar: notification=%d status=%q", calls, a.status)
	}
	a.toastStatus("打不开文件\n第二行", time.Time{}, statusErr)
	if calls != 1 || title != appName || body != "打不开文件 第二行" {
		t.Fatalf("a failure on the bar: %d %q %q", calls, title, body)
	}
	if a.status != "" {
		t.Fatalf("the bar kept a failure pill: %q", a.status)
	}
	// A page is a page: the pill shows, and nothing is sent.
	a.level = 2
	a.toastStatus("已复制", time.Time{}, statusOK)
	if a.status != "已复制" || calls != 1 {
		t.Fatalf("a page toast: status=%q notifications=%d", a.status, calls)
	}
	a.toastStatus("打不开文件", time.Time{}, statusErr)
	if a.statusKind != statusErr || calls != 1 {
		t.Fatalf("a page error toast: kind=%v notifications=%d", a.statusKind, calls)
	}
}
