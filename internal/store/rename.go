package store

import "path/filepath"

// UpdatePaths records that files on disk were renamed: each id → path
// pair replaces the item's Path. Only plain file items are updated —
// managed images and Rakko-converted files live in the data directory and
// are never renamed by this path — and paths are normalized to absolute
// the same way AddFiles does. Unknown ids and non-file items are ignored;
// an id already carrying the path is a no-op. The change is persisted
// atomically in one write; on save failure memory is rolled back. Returns
// how many items changed.
func (s *Store) UpdatePaths(paths map[string]string) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	type undo struct {
		i    int
		path string
	}
	var applied []undo
	for i := range s.Items {
		it := &s.Items[i]
		p, ok := paths[it.ID]
		if !ok || it.Kind != KindFile || it.Converted {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if abs == it.Path {
			continue
		}
		applied = append(applied, undo{i, it.Path})
		it.Path = abs
	}
	if len(applied) == 0 {
		return 0, nil
	}
	if err := s.Save(); err != nil {
		for _, u := range applied {
			s.Items[u.i].Path = u.path
		}
		return 0, err
	}
	return len(applied), nil
}
