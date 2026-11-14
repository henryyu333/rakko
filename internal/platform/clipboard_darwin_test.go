//go:build darwin

package platform

import "testing"

func TestClassifyClipboard(t *testing.T) {
	cases := []struct {
		name  string
		types []string
		want  ClipboardKind
	}{
		{"纯文字", []string{"public.utf8-plain-text"}, ClipboardText},
		{"系统截图", []string{"public.png", "public.tiff"}, ClipboardImage},
		{"密码管理器隐藏标记", []string{"org.nspasteboard.ConcealedType", "public.utf8-plain-text"}, ClipboardSkip},
		{"临时内容标记在后", []string{"public.utf8-plain-text", "org.nspasteboard.TransientType"}, ClipboardSkip},
		{"1Password", []string{"public.utf8-plain-text", "com.agilebits.onepassword"}, ClipboardSkip},
		{"Finder 复制文件：图标与文件名不收", []string{"public.file-url", "public.utf8-plain-text", "public.tiff"}, ClipboardSkip},
		{"表格：文字在前", []string{"public.html", "public.utf16-plain-text", "public.utf8-plain-text", "com.adobe.pdf", "public.tiff"}, ClipboardText},
		{"复制图片：图片在前", []string{"public.tiff", "public.url", "public.utf8-plain-text"}, ClipboardImage},
		{"只有 HTML", []string{"public.html"}, ClipboardSkip},
		{"尚未公布内容", nil, ClipboardNotReady},
	}
	for _, c := range cases {
		if got := ClassifyClipboard(c.types); got != c.want {
			t.Errorf("%s: ClassifyClipboard(%q) = %v, want %v", c.name, c.types, got, c.want)
		}
	}
}
