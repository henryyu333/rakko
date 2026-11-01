package main

import "testing"

func TestClassifyClipboard(t *testing.T) {
	cases := []struct {
		name  string
		types []string
		want  clipKind
	}{
		{"纯文字", []string{"public.utf8-plain-text"}, clipText},
		{"系统截图", []string{"public.png", "public.tiff"}, clipImage},
		{"密码管理器隐藏标记", []string{"org.nspasteboard.ConcealedType", "public.utf8-plain-text"}, clipSkip},
		{"临时内容标记在后", []string{"public.utf8-plain-text", "org.nspasteboard.TransientType"}, clipSkip},
		{"1Password", []string{"public.utf8-plain-text", "com.agilebits.onepassword"}, clipSkip},
		{"Finder 复制文件：图标与文件名不收", []string{"public.file-url", "public.utf8-plain-text", "public.tiff"}, clipSkip},
		{"表格：文字在前", []string{"public.html", "public.utf16-plain-text", "public.utf8-plain-text", "com.adobe.pdf", "public.tiff"}, clipText},
		{"复制图片：图片在前", []string{"public.tiff", "public.url", "public.utf8-plain-text"}, clipImage},
		{"只有 HTML", []string{"public.html"}, clipSkip},
		{"空剪贴板", nil, clipSkip},
	}
	for _, c := range cases {
		if got := classifyClipboard(c.types); got != c.want {
			t.Errorf("%s: classifyClipboard(%q) = %v, want %v", c.name, c.types, got, c.want)
		}
	}
}

// TestClipboardRowsSixVisible: 174pt holds five 30pt rows plus a part of
// the sixth; the list scrolls with the edge blur.
func TestClipboardRowsSixVisible(t *testing.T) {
	if got := clipRowH * 5; got > contentH {
		t.Fatalf("five clipboard rows take %d of %d", got, contentH)
	}
}
