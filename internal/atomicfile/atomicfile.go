// Package atomicfile replaces a file through a temporary file in the same
// directory and a rename, so a crash leaves either the old file or the new
// one — never half of either.
//
// On Windows a rename over a target another process holds open without
// FILE_SHARE_DELETE fails with a sharing violation; replace retries those
// for about a second, which covers an editor, an indexer or a virus scanner
// reading the file at the moment of the save. A target that stays open is
// reported as an error and the old file is left untouched.
package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
)

// WriteFile writes data to dir/name atomically, through a temporary file
// in dir. perm is the temporary file's mode; on Windows only its
// read-only bit survives, as usual. The temporary file is synced to the
// storage device (F_FULLFSYNC on macOS, fsync elsewhere) before the
// replace, and the replace flushes the directory entry afterwards, so a
// crash or a power loss leaves the old contents or the new ones, whole.
func WriteFile(dir, name string, data []byte, perm os.FileMode) error {
	return writeFile(dir, name, data, perm, true)
}

// WriteFileNoSync writes data to dir/name the same way, without syncing the
// temporary file and without changing its mode. It is for a cache rebuilt
// from its source: the write sits on a UI callback, and a crash may lose
// the new contents without losing anything the user cannot get back.
func WriteFileNoSync(dir, name string, data []byte) error {
	return writeFile(dir, name, data, 0, false)
}

// writeFile replaces the target through a temporary file. A zero perm keeps
// the mode CreateTemp gave the file.
func writeFile(dir, name string, data []byte, perm os.FileMode, sync bool) error {
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if perm != 0 {
		if err := tmp.Chmod(perm); err != nil {
			tmp.Close()
			return err
		}
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if sync {
		if err := syncData(tmp); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replace(tmpName, filepath.Join(dir, name))
}

// IsTempName reports whether name follows writeFile's temporary-file
// convention: ".<target>.tmp-<random>", as os.CreateTemp fills the
// pattern in, with a nonempty target name of its own. Open-time cleanup
// removes exactly these leftovers — a killed process's deferred removal
// never ran — and leaves every other file in the directory alone.
func IsTempName(name string) bool {
	if !strings.HasPrefix(name, ".") {
		return false
	}
	return strings.Index(name, ".tmp-") > 1
}
