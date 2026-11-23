package note

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

// AssetsDirName is the subfolder under notes/ where note image assets live.
const AssetsDirName = "assets"

// AssetsDir returns the folder that holds deduplicated image assets for this note store.
func (s *Store) AssetsDir() string {
	return filepath.Join(s.dir, AssetsDirName)
}

// IsSupportedImageExt reports whether ext (with or without leading dot) is an
// accepted image file extension for note attachments.
func IsSupportedImageExt(ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "avif":
		return true
	default:
		return false
	}
}

func normalizeAssetExt(ext string) string {
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

// SaveAsset deduplicates image bytes by SHA-256 (16-hex prefix) into
// notes/assets/ and returns the relative path ("assets/<hash16>.<ext>").
func (s *Store) SaveAsset(data []byte, ext string) (string, error) {
	if s == nil {
		return "", errors.New("note: nil store")
	}
	if len(data) == 0 {
		return "", errors.New("note: empty asset data")
	}
	ext = normalizeAssetExt(ext)
	sum := sha256.Sum256(data)
	hashHex := hex.EncodeToString(sum[:])[:16]
	fileName := hashHex + "." + ext
	assetsDir := s.AssetsDir()
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		return "", fmt.Errorf("note: assets dir: %w", err)
	}
	target := filepath.Join(assetsDir, fileName)
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		if err := atomicfile.WriteFile(assetsDir, fileName, data, 0o600); err != nil {
			return "", fmt.Errorf("note: writing asset: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("note: stat asset: %w", err)
	}
	return filepath.ToSlash(filepath.Join(AssetsDirName, fileName)), nil
}

// SaveAssetFile reads path and stores its bytes under notes/assets/, returning
// the relative path ("assets/<hash16>.<ext>").
func (s *Store) SaveAssetFile(path string) (string, error) {
	ext := filepath.Ext(path)
	if !IsSupportedImageExt(ext) {
		return "", fmt.Errorf("note: unsupported image type %q", ext)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("note: reading image %s: %w", filepath.Base(path), err)
	}
	return s.SaveAsset(data, ext)
}

// AssetPath resolves a relative asset path ("assets/<file>") inside notes/,
// rejecting absolute paths or traversal outside notes/assets/.
func (s *Store) AssetPath(rel string) (string, error) {
	if s == nil {
		return "", errors.New("note: nil store")
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("note: invalid asset path %q", rel)
	}
	assetsDir := s.AssetsDir()
	full := filepath.Join(s.dir, clean)
	r, err := filepath.Rel(assetsDir, full)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("note: asset path %q escapes assets dir", rel)
	}
	return full, nil
}

// FirstImage returns the first Markdown image target in text (e.g.
// "assets/0123456789abcdef.png"), or "" when text has no Markdown image.
func FirstImage(text string) string {
	for _, raw := range strings.Split(text, "\n") {
		for i := 0; i < len(raw); i++ {
			if raw[i] != '!' || i+1 >= len(raw) || raw[i+1] != '[' {
				continue
			}
			dest, next := parseInlineImage(raw, i)
			if next > i && dest != "" {
				return dest
			}
		}
	}
	return ""
}

// stripInlineImages removes Markdown image tokens (![alt](dest)) from a line
// so card titles and preview lines stay readable text beside the thumbnail.
func stripInlineImages(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '!' && i+1 < len(s) && s[i+1] == '[' {
			if _, next := parseInlineImage(s, i); next > i {
				i = next
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func parseInlineImage(s string, start int) (dest string, end int) {
	if start+2 >= len(s) || s[start] != '!' || s[start+1] != '[' {
		return "", start
	}
	i := start + 2
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		if s[i] == ']' {
			break
		}
		i++
	}
	if i+1 >= len(s) || s[i] != ']' || s[i+1] != '(' {
		return "", start
	}
	j := i + 2
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j >= len(s) {
		return "", start
	}
	var rawDest string
	if s[j] == '<' {
		closeIdx := strings.IndexByte(s[j+1:], '>')
		if closeIdx < 0 {
			return "", start
		}
		rawDest = s[j+1 : j+1+closeIdx]
		j = j + 1 + closeIdx + 1
	} else {
		k := j
		depth := 0
		for k < len(s) {
			ch := s[k]
			if ch == '\\' && k+1 < len(s) {
				k += 2
				continue
			}
			if ch == '(' {
				depth++
			} else if ch == ')' {
				if depth == 0 {
					break
				}
				depth--
			} else if ch == ' ' || ch == '\t' {
				break
			}
			k++
		}
		rawDest = s[j:k]
		j = k
	}
	for j < len(s) && s[j] != ')' {
		if s[j] == '\\' && j+1 < len(s) {
			j += 2
			continue
		}
		if s[j] == '"' {
			j++
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				j++
			}
			if j < len(s) {
				j++
			}
			continue
		}
		j++
	}
	if j >= len(s) || s[j] != ')' {
		return "", start
	}
	return strings.TrimSpace(rawDest), j + 1
}
