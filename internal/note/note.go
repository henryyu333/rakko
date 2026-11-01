// Package note keeps the 便利贴 card's Markdown file. One note is one
// plain Markdown file named by a 32-digit hex id in the data directory's
// notes/ folder, and notes/current names the one the card edits — Tern's
// storage model (a shared folder, ids for names, no frontmatter), which
// leaves room for more notes without changing what a note is.
package note

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DirName is the folder inside the data directory.
	DirName = "notes"
	// FileExt is the note file's extension.
	FileExt = ".md"
	// currentFile names the note the card edits, inside DirName.
	currentFile = "current"
	// idLen is the hex id's length: 16 random bytes, as Tern names its
	// notes.
	idLen = 32
)

// Store is the note folder and the id of the one note the card edits.
type Store struct {
	dir string
	id  string
}

// Open returns the store under dataDir, creating notes/, a 32-digit hex
// id and its empty file on first use. A current file that names a note
// which is gone starts a fresh id: a stale name never comes back as a
// blank note under an old name.
func Open(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("note: %w", err)
	}
	s := &Store{dir: dir}
	id, err := os.ReadFile(filepath.Join(dir, currentFile))
	s.id = strings.TrimSpace(string(id))
	if err == nil && validID(s.id) {
		if _, statErr := os.Stat(s.Path()); statErr == nil {
			return s, nil
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("note: reading %s: %w", currentFile, err)
	}
	if s.id, err = NewID(); err != nil {
		return nil, err
	}
	if err := s.writeID(); err != nil {
		return nil, err
	}
	if err := s.Write(""); err != nil {
		return nil, err
	}
	return s, nil
}

// ID is the note's 32-digit hex name.
func (s *Store) ID() string { return s.id }

// Dir is the folder the note lives in.
func (s *Store) Dir() string { return s.dir }

// Path is the note file.
func (s *Store) Path() string { return filepath.Join(s.dir, s.id+FileExt) }

// Read returns the note's text. A note file that went away reads as
// empty, so a card never shows a stale note over a missing file.
func (s *Store) Read() (string, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("note: reading %s: %w", filepath.Base(s.Path()), err)
	}
	return strings.ToValidUTF8(string(data), "\uFFFD"), nil
}

// Write replaces the note's text atomically: a temporary file in the
// note's folder is synced and renamed over the note, so a crash or a
// reader never sees half a note.
func (s *Store) Write(text string) error {
	if err := WriteFile(s.Path(), text); err != nil {
		return fmt.Errorf("note: writing %s: %w", filepath.Base(s.Path()), err)
	}
	return nil
}

// NewID returns a fresh 32-digit hex id.
func NewID() (string, error) {
	var b [idLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("note: id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// writeID records the note's id as the one the card edits.
func (s *Store) writeID() error {
	if err := WriteFile(filepath.Join(s.dir, currentFile), s.id+"\n"); err != nil {
		return fmt.Errorf("note: writing %s: %w", currentFile, err)
	}
	return nil
}

// validID reports whether id is a note name this package writes.
func validID(id string) bool {
	if len(id) != idLen {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// WriteFile replaces path with text atomically: a temporary file beside
// it, synced, then renamed over it. The temporary file is 0600 like the
// rest of the data directory's files.
func WriteFile(path, text string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Title is the note's heading for the window bar and the card: its first
// non-blank line without the Markdown #s and the spaces around it, or ""
// for a note that has nothing but blank lines.
func Title(text string) string {
	title, _ := split(text)
	return title
}

// Preview is up to max non-blank lines that follow the heading — what
// the 2 × 1 card shows under the title.
func Preview(text string, max int) []string {
	_, lines := split(text)
	if max >= 0 && len(lines) > max {
		lines = lines[:max]
	}
	return lines
}

// split returns the heading and the non-blank lines after it, each with
// the whitespace around it trimmed.
func split(text string) (string, []string) {
	var title string
	var lines []string
	first := true
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if first {
			title = strings.TrimSpace(strings.TrimLeft(line, "#"))
			first = false
			continue
		}
		lines = append(lines, line)
	}
	return title, lines
}
