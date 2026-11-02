//go:build darwin

package main

// 「解压」 for archives on the shelf. Zips are extracted with archive/zip
// directly: that keeps raw filename bytes (non-UTF-8 names from
// Windows-made archives — GBK and friends — decode as GB18030), reports
// exact uncompressed sizes for the free-space check, and refuses
// encrypted, absolute-path and ../ archives without running a tool that
// could prompt for a passphrase. Tar-family archives (tar, tgz, tar.gz,
// tar.bz2/tbz, tar.xz) and 7z / rar go through /usr/bin/bsdtar -xf; it
// refuses ../ entries and does not write through a symlink. bsdtar reads
// RAR4/RAR5, but not RAR4 solid archives, encrypted archives, or archive
// parts, so those are reported in plain language instead of surfacing the
// tool's English. Extraction lands in a hidden staging folder inside
// converted/ and is renamed into place only on success, so a failed or
// cancelled run leaves nothing behind. An archive holding one top-level
// folder yields that folder itself, like 归档实用工具.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"rakko/internal/mac"
	"rakko/internal/store"
	"rakko/locales"
)

// archiveExt recognizes the archive filename extensions extraction
// supports: zips, the tar family, and 7z / rar (Finder's own archiver
// reads none of the last two).
func archiveExt(path string) string {
	lower := strings.ToLower(filepath.Base(path))
	for _, ext := range []string{".tar.gz", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	switch filepath.Ext(lower) {
	case ".zip", ".tar", ".tgz", ".tbz", ".tbz2", ".txz", ".7z", ".rar":
		return filepath.Ext(lower)
	}
	return ""
}

// archiveStem is the result folder's name: the file name minus one archive
// extension (「备份.tar.gz」 → 「备份」). A name that is only the extension
// (「.zip」) would leave nothing, so its leading dots are dropped instead
// (「zip」) — never an empty or hidden folder name.
func archiveStem(l locales.Messages, path string) string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, archiveExt(path))
	if strings.Trim(stem, ".") == "" {
		stem = strings.TrimLeft(base, ".")
	}
	if stem == "" {
		return l.ExtractResultFolder()
	}
	return stem
}

// isArchive reports whether item is a regular archive file the extractor
// handles; a folder whose name ends in .zip is not one.
func isArchive(item store.Item) bool {
	if item.Missing || item.Kind != store.KindFile || archiveExt(item.Path) == "" {
		return false
	}
	fi, err := os.Stat(item.Path)
	return err == nil && !fi.IsDir()
}

// Seams: tests substitute the bsdtar runner and the space probe.
var (
	bsdtarRun     = runBsdtar
	freeDiskSpace = realFreeDiskSpace
)

// encryptedErr is the failure of an archive that needs a passphrase:
// reported as such rather than let a tool block on a password prompt.
func encryptedErr(l locales.Messages) error { return errors.New(l.ExtractEncrypted()) }

// safeEntry reports whether an archive member name is safe to extract:
// relative and never escaping the destination.
func safeEntry(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// junkEntry reports whether a member name is Finder noise to drop: any
// __MACOSX/ branch at any depth, and AppleDouble ._ files.
func junkEntry(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "__MACOSX" {
			return true
		}
	}
	return strings.HasPrefix(parts[len(parts)-1], "._")
}

// zipScan opens a zip and validates every member name, returning the
// open reader (its members stay readable until Close), the members to
// extract and their total uncompressed size — or the reason the archive
// must be refused outright: encrypted, corrupt, or carrying unsafe
// entries (a symlink whose target leaves the archive included).
func zipScan(l locales.Messages, src string) (zr *zip.ReadCloser, entries []*zip.File, total int64, err error) {
	zr, err = zip.OpenReader(src)
	if err != nil {
		return nil, nil, 0, errors.New(l.ExtractBadArchive(err.Error()))
	}
	fail := func(err error) (*zip.ReadCloser, []*zip.File, int64, error) {
		zr.Close()
		return nil, nil, 0, err
	}
	for _, f := range zr.File {
		if f.Flags&0x1 != 0 {
			return fail(encryptedErr(l))
		}
		// Checks run on the decoded name: a GBK byte stream can carry
		// 0x5C bytes that only become separators after decoding.
		name := zipName(f)
		if !safeEntry(name) {
			return fail(errors.New(l.ExtractUnsafePath(name)))
		}
		if f.Mode()&os.ModeSymlink != 0 {
			rc, err := f.Open()
			if err != nil {
				return fail(errors.New(l.ExtractReadFailed(err.Error())))
			}
			target, _ := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if !safeEntry(zipText(target)) {
				return fail(errors.New(l.ExtractUnsafeLink(name)))
			}
		}
		if !junkEntry(name) {
			entries = append(entries, f)
			total += int64(f.UncompressedSize64)
		}
	}
	return zr, entries, total, nil
}

// zipText decodes stored bytes: UTF-8 when valid, GB18030 otherwise.
func zipText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	if s, ok := mac.DecodeGB18030(b); ok {
		return s
	}
	return string(b)
}

// zipName renders a member's stored name: flagged-UTF-8 or valid UTF-8
// verbatim, otherwise GB18030 (a superset of GBK, which is what archives
// made on Chinese Windows carry).
func zipName(f *zip.File) string {
	if f.Flags&0x800 != 0 {
		return f.Name
	}
	return zipText([]byte(f.Name))
}

// extractArchive extracts src and renames the result into place at dst —
// or, when the archive holds a single top-level folder, yields that
// folder under a unique name of its own. dst is the archive-named folder
// reserved by planConvert. Cancelling leaves nothing behind.
func extractArchive(l locales.Messages, src, dst string, job *convertJob) (final string, err error) {
	dir := filepath.Dir(dst)
	ext := archiveExt(src)
	var total int64
	var files []*zip.File
	isZip := ext == ".zip"
	if isZip {
		zr, entries, n, err := zipScan(l, src)
		if err != nil {
			return "", err
		}
		defer zr.Close()
		files, total = entries, n
	} else {
		var err error
		if total, err = listArchiveIn(l, src, job); err != nil {
			return "", err
		}
	}
	if total > 0 {
		if free, err := freeDiskSpace(dir); err == nil && total > free {
			return "", errors.New(l.ExtractNoSpace(formatBytes(l, total), formatBytes(l, free)))
		}
	}
	// One extraction runs at a time, so any staging folder already there is
	// a crash leftover; clear it before starting.
	if stale, _ := filepath.Glob(filepath.Join(dir, ".shelf-extract-*")); len(stale) > 0 {
		for _, p := range stale {
			_ = os.RemoveAll(p)
		}
	}
	stage, err := os.MkdirTemp(dir, ".shelf-extract-*")
	if err != nil {
		return "", err
	}
	// Anything short of a successful rename removes the staging folder.
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(stage)
		}
	}()
	if isZip {
		err = extractZipFiles(l, files, stage, job)
	} else {
		err = untarFiles(l, src, stage, job)
	}
	if err != nil {
		return "", err
	}
	if err := pruneJunk(stage); err != nil {
		return "", err
	}
	if err := dropEscapedLinks(stage); err != nil {
		return "", err
	}
	// A single top-level folder becomes the result itself; everything else
	// stays under the archive-named folder.
	tops, err := os.ReadDir(stage)
	if err != nil {
		return "", err
	}
	final = dst
	if len(tops) == 1 && tops[0].IsDir() {
		final = job.extractName(dir, tops[0].Name(), dst)
		if err := os.Rename(filepath.Join(stage, tops[0].Name()), final); err != nil {
			return "", err
		}
		// The staging folder held only the moved folder; drop it too, but
		// never turn a successful result into a failure over it.
		keep = true
		_ = os.Remove(stage)
	} else if err := os.Rename(stage, dst); err != nil {
		return "", err
	}
	keep = true
	return final, nil
}

// extractZipFiles writes the scanned zip members into stage (an
// outside-pointing link was refused by zipScan). Cancelling stops between
// members.
func extractZipFiles(l locales.Messages, files []*zip.File, stage string, job *convertJob) error {
	for _, f := range files {
		if job.cancel.Load() {
			return errConvertCanceled
		}
		name := zipName(f)
		dst := filepath.Join(stage, filepath.FromSlash(name))
		mode := f.Mode()
		switch {
		case f.FileInfo().IsDir():
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case mode&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return errors.New(l.ExtractEntryFailed(name, err.Error()))
			}
			target, _ := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(target), dst); err != nil {
				return errors.New(l.ExtractEntryFailed(name, err.Error()))
			}
		default:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return errors.New(l.ExtractEntryFailed(name, err.Error()))
			}
			w, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm()|0o600)
			if err == nil {
				_, err = io.Copy(w, rc)
				err = errors.Join(err, w.Close())
			}
			rc.Close()
			if err != nil {
				return errors.New(l.ExtractEntryFailed(name, err.Error()))
			}
		}
	}
	return nil
}

// listArchive asks bsdtar for a member listing — also the format check,
// since an unreadable archive fails here before anything is staged — and
// rejects unsafe names. Its errors are in the source language;
// listArchiveIn words them in the language of l.
func listArchive(src string, job *convertJob) (total int64, err error) {
	return listArchiveIn(locales.In(""), src, job)
}

// listArchiveIn is listArchive with its errors in the language of l.
func listArchiveIn(l locales.Messages, src string, job *convertJob) (total int64, err error) {
	out, err := bsdtarRun(job, "-tf", src)
	if err != nil {
		return 0, archiveErr(l, src, err)
	}
	for _, name := range strings.Split(string(out), "\n") {
		name = strings.TrimRight(name, "\r")
		if name == "" {
			continue
		}
		if !safeEntry(name) {
			return 0, errors.New(l.ExtractUnsafePath(name))
		}
	}
	return tarUncompressedSize(src), nil
}

// untarFiles extracts a non-zip archive into stage with bsdtar.
func untarFiles(l locales.Messages, src, stage string, job *convertJob) error {
	if _, err := bsdtarRun(job, "-xf", src, "-C", stage); err != nil {
		return archiveErr(l, src, err)
	}
	return nil
}

// archiveErr maps a bsdtar failure on src to a plain-language error: the
// RAR cases bsdtar cannot handle get their own wording, and anything else
// falls back to its (stderr-derived) message, in the language of l.
func archiveErr(l locales.Messages, src string, err error) error {
	if errors.Is(err, errConvertCanceled) {
		return err
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "encrypt") || strings.Contains(lower, "passphrase") || strings.Contains(lower, "password"):
		return encryptedErr(l)
	case strings.Contains(lower, "solid archive support unavailable"):
		return errors.New(l.ExtractRarSolid())
	case multipartRar(src):
		return errors.New(l.ExtractRarPart())
	case strings.Contains(lower, "truncated") || strings.Contains(lower, "damaged") ||
		strings.Contains(lower, "checksum") || strings.Contains(lower, "too small block"):
		return errors.New(l.ExtractDamaged())
	case strings.Contains(lower, "unrecognized archive format") || strings.Contains(lower, "not supported"):
		return errors.New(l.ExtractUnsupported())
	}
	return errors.New(l.ExtractFailed(err.Error()))
}

// multipartRar reports whether the file name marks one part of a split
// RAR archive: 「x.part1.rar」, or a later volume 「x.r00」/「x.z01」.
func multipartRar(path string) bool {
	if archiveExt(path) != ".rar" {
		ext := filepath.Ext(strings.ToLower(path))
		if len(ext) != 4 || (ext[1] != 'r' && ext[1] != 'z') {
			return false
		}
		_, err := strconv.Atoi(ext[2:])
		return err == nil
	}
	lower := strings.ToLower(filepath.Base(path))
	if i := strings.LastIndex(lower, ".part"); i >= 0 {
		_, err := strconv.Atoi(strings.TrimSuffix(lower[i+5:], ".rar"))
		return err == nil
	}
	return false
}

// tarUncompressedSize sums member sizes of a plain/gzip/bzip2 tar with the
// standard library. xz tars, 7z and rar cannot be read here, so they report
// 0 and the caller skips the free-space check for them.
func tarUncompressedSize(src string) (total int64) {
	f, err := os.Open(src)
	if err != nil {
		return 0
	}
	defer f.Close()
	var r io.Reader = f
	switch archiveExt(src) {
	case ".tgz", ".tar.gz":
		if r, err = gzip.NewReader(f); err != nil {
			return 0
		}
	case ".tbz", ".tbz2", ".tar.bz2":
		r = bzip2.NewReader(f)
	case ".tar":
	default:
		return 0
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err != nil {
			return total
		}
		total += h.Size
	}
}

// pruneJunk removes __MACOSX branches at any depth and ._ AppleDouble
// files that made it into stage (zips are filtered at scan time; this is
// for tar).
func pruneJunk(stage string) error {
	return filepath.WalkDir(stage, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != stage && d.Name() == "__MACOSX" {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), "._") {
			return os.Remove(p)
		}
		return nil
	})
}

// dropEscapedLinks unlinks symbolic links whose target resolves outside
// stage (absolute targets or relative paths that escape); in-archive
// links are kept. bsdtar already refuses to write through one, so nothing
// under an outside link can have been written.
func dropEscapedLinks(stage string) error {
	root, err := filepath.EvalSymlinks(stage)
	if err != nil {
		return err
	}
	return filepath.WalkDir(stage, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil // a dangling link points nowhere; it cannot leak
		}
		if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
			return nil
		}
		return os.Remove(p)
	})
}

// extractName picks the result folder's name for a single-top-folder
// archive: it avoids every name the rest of this batch already reserved
// (the archive's own planned folder aside — the result may take that very
// name) as well as what is already on disk.
func (j *convertJob) extractName(dir, name, dst string) string {
	taken := j.taken
	if taken == nil {
		return uniqueDir(dir, name, nil)
	}
	own := taken[dst]
	delete(taken, dst)
	final := uniqueDir(dir, name, taken)
	if own {
		taken[dst] = true
	}
	return final
}

// uniqueDir reserves dir/name — or 「name 2」, … when taken — and returns
// the full path without creating it.
func uniqueDir(dir, name string, taken map[string]bool) string {
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s %d", name, n)
		}
		p := filepath.Join(dir, candidate)
		if taken != nil {
			if taken[p] {
				continue
			}
			taken[p] = true
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		return p
	}
}

// runBsdtar runs /usr/bin/bsdtar with its streams captured separately: a
// listing goes to stdout and is returned, while an error comes from stderr
// only, so a failed listing never turns its file names into the message.
// A cancelled job kills the process. Stdin stays closed so a passphrase
// request can never block (encrypted archives are refused before this
// runs).
func runBsdtar(job *convertJob, args ...string) ([]byte, error) {
	cmd := exec.Command("/usr/bin/bsdtar", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	if job != nil {
		go func() {
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					if job.cancel.Load() {
						_ = cmd.Process.Kill()
					}
				}
			}
		}()
	}
	err := cmd.Wait()
	close(done)
	if job != nil && job.cancel.Load() {
		return stdout.Bytes(), errConvertCanceled
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), errors.New(msg)
	}
	return stdout.Bytes(), nil
}

// realFreeDiskSpace reports the free bytes on the volume holding dir.
func realFreeDiskSpace(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// addExtracted registers freshly extracted folders as tray items.
// AddConverted only takes regular files, so this runs through AddFiles
// (which accepts folders and mints ids) and then marks the new items
// Converted, matching AddConverted's contract: the path must be a
// directory directly inside converted/, the items lead the tray in order,
// and removal paths treat them as shelf-owned.
func addExtracted(s *store.Store, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	dir := s.ConvertedDir()
	resolvedDir := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolvedDir = r
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		abs[i], _ = filepath.Abs(p)
		r, err := filepath.EvalSymlinks(abs[i])
		if err != nil || filepath.Dir(r) != resolvedDir {
			return nil, fmt.Errorf("store: extracted path %q is not inside %s", p, dir)
		}
		fi, err := os.Stat(r)
		if err != nil {
			return nil, fmt.Errorf("store: extracted folder %q: %w", p, err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("store: extracted path %q is not a folder", p)
		}
	}
	want := map[string]bool{}
	for _, p := range abs {
		want[p] = true // AddFiles stores filepath.Abs, unresolved
	}
	// AddFiles puts paths[last] first; feeding them reversed keeps the
	// outputs in plan order at the front.
	rev := make([]string, len(abs))
	for i := range abs {
		rev[i] = abs[len(abs)-1-i]
	}
	if err := s.AddFiles(rev); err != nil {
		return nil, err
	}
	old := append([]store.Item(nil), s.Items...)
	ids := make([]string, 0, len(abs))
	for i := range s.Items {
		if !want[s.Items[i].Path] {
			continue
		}
		s.Items[i].Converted = true
		ids = append(ids, s.Items[i].ID)
	}
	if len(ids) != len(abs) {
		s.Items = old
		return nil, fmt.Errorf("store: extracted items did not land")
	}
	if err := s.Save(); err != nil {
		s.Items = old
		return nil, err
	}
	return ids, nil
}
