//go:build darwin

package main

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"rakko/locales"
)

// writeZip archives srcs — files and whole folders — into dst, each at
// the archive's root under the matching name in roots, as Finder's
// 压缩 does: folders keep their structure, symbolic links are stored as
// links (never followed), permissions and modification times are kept,
// and .DS_Store files are left out. job.total is set to the number of
// entries and job.done advances per entry; cancelling stops between
// entries with errConvertCanceled. The archive is written to a temporary
// file beside dst and renamed into place, so a failed or cancelled run
// leaves nothing behind. An entry's failure is worded in the source
// language; writeZipIn words it in the language of l.
func writeZip(dst string, srcs, roots []string, job *convertJob) (err error) {
	return writeZipIn(locales.In(""), dst, srcs, roots, job)
}

// writeZipIn is writeZip with its errors in the language of l.
func writeZipIn(l locales.Messages, dst string, srcs, roots []string, job *convertJob) (err error) {
	total := 0
	for _, src := range srcs {
		if err := filepath.WalkDir(src, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if job.cancel.Load() {
				return errConvertCanceled
			}
			if zippable(d) {
				total++
			}
			return nil
		}); err != nil {
			return err
		}
	}
	job.total.Store(int32(max(total, 1)))

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".shelf-zip-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	self, err := tmp.Stat()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(tmp)
	for i, src := range srcs {
		err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if job.cancel.Load() {
				return errConvertCanceled
			}
			if !zippable(d) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			// Never read the archive being written: zipping a folder that
			// holds it would grow it without end.
			if os.SameFile(info, self) {
				return nil
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			name := roots[i]
			if rel != "." {
				name = path.Join(name, filepath.ToSlash(rel))
			}
			if err := addZipEntry(zw, p, name, info); err != nil {
				return errors.New(l.ZipperEntry(name, err.Error()))
			}
			job.done.Add(1)
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// zippable reports whether an entry goes into an archive: folders,
// regular files and symbolic links, but not .DS_Store or special files.
func zippable(d fs.DirEntry) bool {
	if d.Name() == ".DS_Store" {
		return false
	}
	t := d.Type()
	return t.IsDir() || t.IsRegular() || t&fs.ModeSymlink != 0
}

// addZipEntry writes one entry: a folder as name/, a link as its target,
// a file's contents deflated.
func addZipEntry(zw *zip.Writer, p, name string, info fs.FileInfo) error {
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = name
	switch {
	case info.IsDir():
		hdr.Name += "/"
		hdr.Method = zip.Store
		_, err = zw.CreateHeader(hdr)
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		hdr.Method = zip.Store
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, target)
		return err
	}
	hdr.Method = zip.Deflate
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
