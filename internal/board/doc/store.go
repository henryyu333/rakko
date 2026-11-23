package doc

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rakko/internal/atomicfile"
)

const (
	// DirName is the folder inside Rakko's data directory for whiteboards.
	DirName = "boards"
	// AssetsDirName stores content-addressed image assets inside DirName.
	AssetsDirName = "assets"
	// FileExt is the board JSON file extension.
	FileExt = ".json"
	// currentFile stores the active board id inside DirName.
	currentFile = "current"
	// idLen is the 32-hex-char file identifier length.
	idLen = 32
)

// ErrLastBoard is returned when trying to delete the only remaining board.
var ErrLastBoard = errors.New("board: cannot delete the last board")

// BoardInfo summarizes one board file on disk for the switcher menu.
type BoardInfo struct {
	ID        string
	Name      string
	Path      string
	ItemCount int
	Mod       time.Time
}

// TrashFunc moves a file to the OS trash/recycle bin.
type TrashFunc func(path string) (string, error)

// Store manages boards/, boards/current, and boards/assets/ under dataDir.
type Store struct {
	dir         string
	id          string
	defaultName string
}

// Open opens or initializes dataDir/boards/ and selects the current board.
// If current points to a missing board, it falls back to the most recent board
// or creates a fresh default board. An unreadable current file or directory
// returns an error instead of overwriting existing data.
func Open(dataDir string, defaultName string) (*Store, error) {
	if defaultName == "" {
		defaultName = "Board 1"
	}
	dir := filepath.Join(dataDir, DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("board: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, AssetsDirName), 0o700); err != nil {
		return nil, fmt.Errorf("board: %w", err)
	}
	s := &Store{dir: dir, defaultName: defaultName}
	rawID, err := os.ReadFile(filepath.Join(dir, currentFile))
	s.id = strings.TrimSpace(string(rawID))
	if err == nil && validBoardID(s.id) {
		_, statErr := os.Stat(s.Path())
		if statErr == nil {
			return s, nil
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("board: %w", statErr)
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("board: reading %s: %w", currentFile, err)
	}

	if list, lerr := s.List(); lerr == nil && len(list) > 0 {
		s.id = list[0].ID
		if err := s.writeCurrent(); err != nil {
			return nil, err
		}
		return s, nil
	}

	id, err := newBoardID()
	if err != nil {
		return nil, err
	}
	s.id = id
	d := NewDrawing(id, s.defaultName)
	if err := s.Write(d); err != nil {
		return nil, err
	}
	if err := s.writeCurrent(); err != nil {
		return nil, err
	}
	return s, nil
}

// ID returns the active board's 32-char hex identifier.
func (s *Store) ID() string { return s.id }

// Dir returns the boards/ directory path.
func (s *Store) Dir() string { return s.dir }

// AssetsDir returns the boards/assets/ directory path.
func (s *Store) AssetsDir() string { return filepath.Join(s.dir, AssetsDirName) }

// Path returns the active board JSON file path.
func (s *Store) Path() string { return filepath.Join(s.dir, s.id+FileExt) }

// PathFor returns the JSON file path for board id.
func (s *Store) PathFor(id string) string { return filepath.Join(s.dir, id+FileExt) }

// Read loads and decodes the active board from disk.
func (s *Store) Read() (*Drawing, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return nil, fmt.Errorf("board: reading %s: %w", filepath.Base(s.Path()), err)
	}
	var d Drawing
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("board: parsing %s: %w", filepath.Base(s.Path()), err)
	}
	d.Normalize(s.id, s.defaultName)
	return &d, nil
}

// Write atomically persists d to the active board file.
func (s *Store) Write(d *Drawing) error {
	return s.WriteFor(s.id, d)
}

// WriteFor atomically persists d to the board file for id without switching active board.
func (s *Store) WriteFor(id string, d *Drawing) error {
	if !validBoardID(id) {
		return fmt.Errorf("board: %q is not a valid board id", id)
	}
	if d == nil {
		return errors.New("board: nil drawing")
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("board: encoding %s: %w", id+FileExt, err)
	}
	data = append(data, '\n')
	if err := atomicfile.WriteFile(s.dir, id+FileExt, data, 0o600); err != nil {
		return fmt.Errorf("board: writing %s: %w", id+FileExt, err)
	}
	return nil
}

// List returns metadata for all valid board files in boards/, newest first.
func (s *Store) List() ([]BoardInfo, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("board: %w", err)
	}
	out := make([]BoardInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), FileExt) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), FileExt)
		if !validBoardID(id) {
			continue
		}
		fullPath := filepath.Join(s.dir, e.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		var meta struct {
			Name   string              `json:"name"`
			Shapes map[ID]json.RawMessage `json:"shapes"`
		}
		name := s.defaultName
		count := 0
		if json.Unmarshal(data, &meta) == nil {
			if strings.TrimSpace(meta.Name) != "" {
				name = meta.Name
			}
			count = len(meta.Shapes)
		}
		var mod time.Time
		if info, err := e.Info(); err == nil {
			mod = info.ModTime()
		}
		out = append(out, BoardInfo{
			ID:        id,
			Name:      name,
			Path:      fullPath,
			ItemCount: count,
			Mod:       mod,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Mod.Equal(out[j].Mod) {
			return out[i].Mod.After(out[j].Mod)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Select switches the active board to id and updates boards/current.
func (s *Store) Select(id string) error {
	if !validBoardID(id) {
		return fmt.Errorf("board: %q is not a valid board id", id)
	}
	if _, err := os.Stat(s.PathFor(id)); err != nil {
		return fmt.Errorf("board: %s: %w", id+FileExt, err)
	}
	prev := s.id
	s.id = id
	if err := s.writeCurrent(); err != nil {
		s.id = prev
		return err
	}
	return nil
}

// New creates a fresh board file on disk and makes it the active board.
func (s *Store) New(name string) (*Drawing, error) {
	id, err := newBoardID()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = s.defaultName
	}
	d := NewDrawing(id, name)
	prev := s.id
	s.id = id
	if err := s.Write(d); err != nil {
		s.id = prev
		return nil, err
	}
	if err := s.writeCurrent(); err != nil {
		s.id = prev
		return nil, err
	}
	return d, nil
}

// Trash moves board id to the OS trash via trashFn, refusing to delete the last board.
// If id was the active board, current is switched to another surviving board.
func (s *Store) Trash(id string, trashFn TrashFunc) error {
	if !validBoardID(id) {
		return fmt.Errorf("board: %q is not a valid board id", id)
	}
	if trashFn == nil {
		return errors.New("board: trash function is required")
	}
	list, err := s.List()
	if err != nil {
		return err
	}
	if len(list) <= 1 {
		return ErrLastBoard
	}
	var fallback string
	found := false
	for _, b := range list {
		if b.ID == id {
			found = true
			continue
		}
		if fallback == "" {
			fallback = b.ID
		}
	}
	if !found {
		return fmt.Errorf("board: %q not found", id)
	}
	if _, err := trashFn(s.PathFor(id)); err != nil {
		return fmt.Errorf("board: trash %s: %w", id, err)
	}
	if s.id == id {
		s.id = fallback
		if err := s.writeCurrent(); err != nil {
			return err
		}
	}
	return nil
}

// SaveAsset deduplicates image bytes by SHA-256 (16-hex prefix) into boards/assets/
// and returns the relative asset descriptor ("assets/<hash16>.<ext>").
func (s *Store) SaveAsset(data []byte, ext, name, mime string, w, h float64) (*Asset, error) {
	if len(data) == 0 {
		return nil, errors.New("board: empty asset data")
	}
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	if ext == "" {
		ext = "png"
	}
	for _, r := range ext {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			ext = "png"
			break
		}
	}
	sum := sha256.Sum256(data)
	hashHex := hex.EncodeToString(sum[:])[:16]
	fileName := hashHex + "." + ext
	assetsDir := s.AssetsDir()
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		return nil, fmt.Errorf("board: assets dir: %w", err)
	}
	target := filepath.Join(assetsDir, fileName)
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		if err := atomicfile.WriteFile(assetsDir, fileName, data, 0o600); err != nil {
			return nil, fmt.Errorf("board: writing asset: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("board: stat asset: %w", err)
	}
	rel := filepath.ToSlash(filepath.Join(AssetsDirName, fileName))
	return &Asset{
		ID:   ID("asset:" + hashHex),
		Type: "image",
		W:    w,
		H:    h,
		Name: name,
		Mime: mime,
		Hash: hashHex,
		Path: rel,
	}, nil
}

// AssetPath resolves a relative asset path ("assets/<file>") inside boards/,
// rejecting path traversal outside boards/assets/.
func (s *Store) AssetPath(rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("board: invalid asset path %q", rel)
	}
	assetsDir := s.AssetsDir()
	full := filepath.Join(s.dir, clean)
	r, err := filepath.Rel(assetsDir, full)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("board: asset path %q escapes assets dir", rel)
	}
	return full, nil
}

func (s *Store) writeCurrent() error {
	if err := atomicfile.WriteFile(s.dir, currentFile, []byte(s.id+"\n"), 0o600); err != nil {
		return fmt.Errorf("board: writing %s: %w", currentFile, err)
	}
	return nil
}

func newBoardID() (string, error) {
	var b [idLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("board: id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func validBoardID(id string) bool {
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
