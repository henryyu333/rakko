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
)

// WriteFile writes data to dir/name atomically, through a temporary file in
// dir. perm is the temporary file's mode; on Windows only its read-only bit
// survives, as usual.
func WriteFile(dir, name string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	return replace(tmpName, filepath.Join(dir, name))
}
