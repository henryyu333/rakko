package main

import (
	"crypto/sha256"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// boardClipboardFingerprint identifies the system clipboard's contents at the
// moment the board copied shapes. A paste compares it with the clipboard now:
// unchanged means the user still wants the board's own clipboard, changed means
// something newer (a screenshot, text) takes the paste.
var boardClipboardFingerprint = func(c *ui.Context) string {
	if c == nil {
		return ""
	}
	svc := c.Services()
	formats := svc.ClipboardFormats()
	var b strings.Builder
	for _, f := range formats {
		b.WriteString(string(f))
		b.WriteByte(0)
	}
	var want []transfer.Format
	for _, f := range []transfer.Format{transfer.Text, transfer.HTML, transfer.PNG, transfer.URIList} {
		for _, have := range formats {
			if have == f {
				want = append(want, f)
				break
			}
		}
	}
	if len(want) == 0 {
		return b.String()
	}
	data := svc.ReadClipboardData(want...)
	for _, f := range want {
		raw, err := data.Read(f)
		if err != nil || len(raw) == 0 {
			continue
		}
		sum := sha256.Sum256(raw)
		b.WriteString(string(f))
		b.Write(sum[:])
	}
	return b.String()
}

// copySelection copies to the board's own clipboard and remembers which system
// clipboard contents were there, so an unchanged clipboard keeps pasting the
// shapes rather than an old image.
func (b *boardUI) copySelection(c *ui.Context) bool {
	if b.ed == nil || !b.ed.Copy() {
		return false
	}
	b.clipStamp = boardClipboardFingerprint(c)
	return true
}

func (b *boardUI) cutSelection(c *ui.Context) bool {
	if b.ed == nil || !b.ed.Cut() {
		return false
	}
	b.clipStamp = boardClipboardFingerprint(c)
	return true
}

func (b *boardUI) internalClipboardCurrent(c *ui.Context) bool {
	return b.ed != nil && b.ed.HasClipboard() && b.clipStamp == boardClipboardFingerprint(c)
}
