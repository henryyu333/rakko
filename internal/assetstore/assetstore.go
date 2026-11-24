// Package assetstore is the content-addressed image folder shared by notes and
// boards: SHA-256 of the bytes names the file, so saving the same picture twice
// costs no space.
package assetstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"rakko/internal/atomicfile"
)

// Subdir is the folder name assets live in, relative to a store root.
const Subdir = "assets"

// NormalizeExt keeps a simple lowercase extension, defaulting to png.
func NormalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	if ext == "" {
		return "png"
	}
	for _, r := range ext {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return "png"
		}
	}
	return ext
}

// Save deduplicates data into dir and returns the file name and its 16-hex
// hash. dir is the assets folder itself.
func Save(dir string, data []byte, ext string) (name, hash string, err error) {
	if len(data) == 0 {
		return "", "", errors.New("assetstore: empty data")
	}
	ext = NormalizeExt(ext)
	sum := sha256.Sum256(data)
	hash = hex.EncodeToString(sum[:])[:16]
	name = hash + "." + ext
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("assetstore: dir: %w", err)
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		if err := atomicfile.WriteFile(dir, name, data, 0o600); err != nil {
			return "", "", fmt.Errorf("assetstore: writing: %w", err)
		}
	} else if err != nil {
		return "", "", fmt.Errorf("assetstore: stat: %w", err)
	}
	return name, hash, nil
}

// Resolve turns "assets/<file>" into a path under base's assets folder and
// rejects traversal outside it.
func Resolve(base, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("assetstore: invalid path %q", rel)
	}
	assetsDir := filepath.Join(base, Subdir)
	full := filepath.Join(base, clean)
	r, err := filepath.Rel(assetsDir, full)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("assetstore: path %q escapes assets", rel)
	}
	return full, nil
}

// Rel returns the relative path of a stored file name.
func Rel(name string) string { return filepath.ToSlash(filepath.Join(Subdir, name)) }
