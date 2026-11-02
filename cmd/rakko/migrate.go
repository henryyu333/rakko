//go:build darwin

package main

// The app used to keep its data in Library/Application Support/Shelf, then in
// Library/Application Support/ShelfU. The current app reads
// Library/Application Support/Rakko, so the first launch copies the newest
// old directory over once and leaves the old one as a backup: the user's
// history, managed images and converted files arrive intact.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// errLegacyRootLocked reports that another process (the old app) holds the
// old data directory. Migration must not touch a live directory, and the
// caller tells the user to quit the old app first.
var errLegacyRootLocked = errors.New("migrate: the old data directory is in use by another instance")

// legacyLockFile is the advisory lock store.Open takes on a data directory;
// the migration checks it without creating or rewriting it.
const legacyLockFile = ".lock"

// migratedJSONFiles are the files inside a data root that can hold absolute
// paths into that root: the shelf state (item paths), the layout (pinned
// cards) and the preferences. The names match store, layout and settings.
var migratedJSONFiles = []string{"state.json", "layout.json", "settings.json"}

// migrateDataRoot copies the data directory oldRoot to newRoot, rewriting
// absolute paths that pointed inside oldRoot. It reports whether it
// migrated.
//
// The rules keep a user's history safe:
//   - newRoot exists: it wins, nothing is copied (a later migration would
//     overwrite newer data).
//   - oldRoot is missing: nothing to do.
//   - another process holds oldRoot's lock: no migration, errLegacyRootLocked.
//   - the copy is built in a sibling temporary directory (APFS clones, so
//     the data blocks are shared), then renamed into place in one step.
//   - any failure removes the temporary directory, leaves oldRoot intact
//     and newRoot absent, and returns the error: the app must never start
//     with empty data.
//
// oldRoot is never written to, so it stays as a complete backup.
func migrateDataRoot(oldRoot, newRoot string) (bool, error) {
	if oldRoot == "" || newRoot == "" {
		return false, errors.New("migrate: empty data directory")
	}
	oldRoot = filepath.Clean(oldRoot)
	newRoot = filepath.Clean(newRoot)
	if oldRoot == newRoot {
		return false, nil
	}
	if _, err := os.Stat(newRoot); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("migrate: checking %s: %w", newRoot, err)
	}
	fi, err := os.Stat(oldRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("migrate: checking %s: %w", oldRoot, err)
	}
	if !fi.IsDir() {
		return false, nil
	}
	unlock, err := lockLegacyRoot(oldRoot)
	if err != nil {
		return false, err
	}
	defer unlock()

	temp, err := os.MkdirTemp(filepath.Dir(newRoot), "."+filepath.Base(newRoot)+".migrating-*")
	if err != nil {
		return false, fmt.Errorf("migrate: creating the temporary directory: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(temp)
		}
	}()

	if err := copyCloned(oldRoot, temp); err != nil {
		return false, err
	}
	// The lock belongs to the old directory, not to the copied data: the
	// new root takes its own lock when the store opens it.
	if err := os.Remove(filepath.Join(temp, legacyLockFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("migrate: dropping the copied lock file: %w", err)
	}
	if err := rewriteRootPaths(temp, oldRoot, newRoot); err != nil {
		return false, err
	}
	if err := os.Rename(temp, newRoot); err != nil {
		return false, fmt.Errorf("migrate: moving the copy into %s: %w", newRoot, err)
	}
	done = true
	return true, nil
}

// lockLegacyRoot takes the same exclusive advisory lock store.Open takes,
// without creating or rewriting the lock file, so a running old app is
// detected. A missing lock file means no instance ever opened the old
// directory, so there is nothing to conflict with. The returned function
// releases the lock.
func lockLegacyRoot(root string) (func(), error) {
	path := filepath.Join(root, legacyLockFile)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrate: opening %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLegacyRootLocked
		}
		return nil, fmt.Errorf("migrate: locking %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// copyCloned copies the contents of src into the existing directory dst:
// APFS clones share the data blocks, so the migration does not need a
// second copy's worth of free disk. A filesystem without clone support
// falls back to a plain copy of everything.
func copyCloned(src, dst string) error {
	// The trailing "/." copies the contents of src rather than src itself.
	// filepath.Join would clean it away, so spell it out.
	from := src + string(filepath.Separator) + "."
	if out, err := exec.Command("/bin/cp", "-cRp", from, dst).CombinedOutput(); err != nil {
		// A partial first attempt could leave files behind that the second
		// attempt would mix with; start from an empty directory again.
		if cerr := clearDir(dst); cerr != nil {
			return fmt.Errorf("migrate: copying %s: %v: %s", src, cerr, strings.TrimSpace(string(out)))
		}
		out, err = exec.Command("/bin/cp", "-Rp", from, dst).CombinedOutput()
		if err != nil {
			return fmt.Errorf("migrate: copying %s: %w: %s", src, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// clearDir removes the entries of dir, keeping the directory itself.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// rewriteRootPaths rewrites every absolute path inside the JSON data files
// of root that points into oldRoot so it points into newRoot. Files that
// cannot be parsed are left alone: the app already falls back to defaults
// for them, and a broken file must not block the migration.
func rewriteRootPaths(root, oldRoot, newRoot string) error {
	relocate := pathRelocator(oldRoot, newRoot)
	for _, name := range migratedJSONFiles {
		path := filepath.Join(root, name)
		fi, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("migrate: checking %s: %w", path, err)
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("migrate: reading %s: %w", path, err)
		}
		doc, ok := decodeJSON(data)
		if !ok {
			log.Printf("%s: %s 不是有效的 JSON，迁移时保持原样", appName, path)
			continue
		}
		if !rewriteJSONStrings(doc, relocate) {
			continue
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return fmt.Errorf("migrate: encoding %s: %w", path, err)
		}
		if err := os.WriteFile(path, buf.Bytes(), fi.Mode().Perm()); err != nil {
			return fmt.Errorf("migrate: writing %s: %w", path, err)
		}
	}
	return nil
}

// decodeJSON parses one JSON document, keeping numbers as written so a
// rewritten file stays byte-for-byte the same apart from the paths.
func decodeJSON(data []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, false
	}
	return doc, true
}

// rewriteJSONStrings applies rewrite to every string inside the containers
// of v and reports whether anything changed. A document whose root is a
// plain string cannot be changed in place, so it reports false.
func rewriteJSONStrings(v any, rewrite func(string) string) bool {
	switch x := v.(type) {
	case []any:
		changed := false
		for i, e := range x {
			if s, ok := e.(string); ok {
				if n := rewrite(s); n != s {
					x[i] = n
					changed = true
				}
				continue
			}
			if rewriteJSONStrings(e, rewrite) {
				changed = true
			}
		}
		return changed
	case map[string]any:
		changed := false
		for k, e := range x {
			if s, ok := e.(string); ok {
				if n := rewrite(s); n != s {
					x[k] = n
					changed = true
				}
				continue
			}
			if rewriteJSONStrings(e, rewrite) {
				changed = true
			}
		}
		return changed
	}
	return false
}

// pathRelocator returns the function that moves an absolute path below
// oldRoot to the same place below newRoot. The old root is also matched in
// its symlink-resolved spelling, which is what the store persisted when the
// old directory was reached through one.
func pathRelocator(oldRoot, newRoot string) func(string) string {
	prefixes := [][2]string{{oldRoot, newRoot}}
	if resolved, err := filepath.EvalSymlinks(oldRoot); err == nil && resolved != oldRoot {
		prefixes = append(prefixes, [2]string{resolved, newRoot})
	}
	return func(p string) string {
		for _, pair := range prefixes {
			old, next := pair[0], pair[1]
			if p == old {
				return next
			}
			if strings.HasPrefix(p, old+string(filepath.Separator)) {
				return next + p[len(old):]
			}
		}
		return p
	}
}
