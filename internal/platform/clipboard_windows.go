//go:build windows

package platform

// Clipboard. The classification itself is portable and the Windows format
// names are real; reading the clipboard needs the OLE clipboard, which the
// clipboard batch owns. WINDOWS-NEXT(clipboard)

import "slices"

// ClipboardKind is what one clipboard change should become.
type ClipboardKind int

const (
	ClipboardNotReady ClipboardKind = iota // no type declared yet
	ClipboardSkip                          // private, files, or nothing usable
	ClipboardText
	ClipboardImage
)

var (
	privateClipTypes = []string{
		"ExcludeClipboardContentFromMonitorProcessing",
		"CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard",
		"Clipboard Viewer Ignore",
	}
	clipTextTypes  = []string{"CF_UNICODETEXT", "CF_TEXT", "CF_OEMTEXT"}
	clipImageTypes = []string{"CF_DIBV5", "CF_DIB", "PNG", "image/png"}
	clipFileTypes  = []string{"CF_HDROP", "FileGroupDescriptorW", "FileGroupDescriptor"}
)

// ClassifyClipboard decides what a clipboard change should add, from the
// declared formats in the source's order of preference.
func ClassifyClipboard(types []string) ClipboardKind {
	if len(types) == 0 {
		return ClipboardNotReady
	}
	for _, t := range types {
		if slices.Contains(privateClipTypes, t) {
			return ClipboardSkip
		}
	}
	for _, t := range types {
		if slices.Contains(clipFileTypes, t) {
			return ClipboardSkip
		}
	}
	for _, t := range types {
		switch {
		case slices.Contains(clipTextTypes, t):
			return ClipboardText
		case slices.Contains(clipImageTypes, t):
			return ClipboardImage
		}
	}
	return ClipboardSkip
}

// ClipboardChangeCount is the sequence number that increments for every
// clipboard mutation system-wide. WINDOWS-NEXT(clipboard)
func ClipboardChangeCount() int64 { return 0 }

// ClipboardChangeCountFast reads the same counter from a worker thread.
// WINDOWS-NEXT(clipboard)
func ClipboardChangeCountFast() int64 { return 0 }

// ClipboardTypes lists the formats the clipboard declares.
// WINDOWS-NEXT(clipboard)
func ClipboardTypes() []string { return nil }

// PrimeClipboardChangeCount prepares the worker-thread read.
// WINDOWS-NEXT(clipboard)
func PrimeClipboardChangeCount() {}
