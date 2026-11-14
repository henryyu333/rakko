//go:build windows

package main

// Browser download bundles. Chrome, Edge and Firefox all publish their
// partial downloads as `.crdownload`/`.part` files, which the shared
// scanner already reads; Safari's `.download` bundle has no Windows
// equivalent, so this hook never fires.
func safariDownload(bundle string) (downloadFile, bool) {
	return downloadFile{}, false
}
