package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"rakko/internal/atomicfile"
)

// ErrStateUnreadable marks a state.json Open refused to read because the
// file cannot be parsed: the bytes are not the format, truncated or
// corrupt. The file is intact on disk and Open never overwrites it; the
// recovery dialog is the way out of it.
var ErrStateUnreadable = errors.New("store: state file is unreadable")

// ErrStateNewer marks a state.json Open refused to read because a newer
// Rakko wrote it: a state version above this build's, or an item kind
// this build does not know. Nothing is wrong with the user's data —
// this build is simply too old for it — so recovery offers nothing:
// the launch says so and the user upgrades.
var ErrStateNewer = errors.New("store: state file was written by a newer Rakko")

// corruptTimeLayout names the moved-aside unreadable original:
// state.json.corrupt-<20060102-150405>. The bad file is never deleted
// and never overwritten; whatever else the user picks, it stays.
const corruptTimeLayout = "20060102-150405"

// parseState validates the bytes of a state.json and returns its parsed
// contents. Open and the recovery paths agree through it on what
// "readable" means: the JSON parses, the version is one this build
// understands, and every item is a kind this build knows — a state a
// newer Rakko wrote must be refused as newer, not silently trimmed.
func parseState(data []byte) (stateFile, error) {
	var sf stateFile
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &sf); err != nil {
		return sf, fmt.Errorf("%w: parsing %s: %v", ErrStateUnreadable, stateFileName, err)
	}
	if sf.Version > stateVersion {
		return sf, fmt.Errorf("%w: %s holds state version %d, this Rakko understands %d",
			ErrStateNewer, stateFileName, sf.Version, stateVersion)
	}
	if sf.Version != stateVersion {
		// 低于当前版本的文件只会来自残缺或手改：按坏文件走恢复路径。
		return sf, fmt.Errorf("%w: %s holds state version %d, this Rakko understands %d",
			ErrStateUnreadable, stateFileName, sf.Version, stateVersion)
	}
	for _, it := range sf.Items {
		switch it.Kind {
		case KindText, KindImage, KindFile:
		default:
			return sf, fmt.Errorf("%w: %s holds a %q item a newer Rakko wrote; upgrade rather than let this one erase it",
				ErrStateNewer, stateFileName, it.Kind)
		}
	}
	return sf, nil
}

// RecoveryBackups lists the readable state.json backups in root, newest
// first: the .bak series a migration keeps, each one parseable and of the
// version this build understands. It is the menu of what "restore" can
// bring back after Open returned ErrStateUnreadable; an empty list
// leaves starting over as the only choice.
func RecoveryBackups(root string) []string {
	type backup struct {
		name string
		mod  time.Time
	}
	var found []backup
	for _, name := range migrationBackupNames {
		path := filepath.Join(root, name)
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if _, perr := parseState(data); perr != nil {
			continue
		}
		found = append(found, backup{name: name, mod: fi.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	names := make([]string, len(found))
	for i, b := range found {
		names[i] = b.name
	}
	return names
}

// RecoverFailure reports a recovery that could not put the corrupt original
// back where it was: the backup write failed and moving the original from
// Aside to state.json failed too. Aside names the file inside the data
// directory, so the launch can tell the user where their history is.
type RecoverFailure struct {
	Aside string
	Err   error
}

func (e *RecoverFailure) Error() string { return e.Err.Error() }
func (e *RecoverFailure) Unwrap() error { return e.Err }

// writeStateFile is atomicfile.WriteFile, named here so a test can make the
// disk fail at exactly this step of a recovery.
var writeStateFile = atomicfile.WriteFile

// RecoverState is the user's way out of an unreadable state.json. The
// corrupt original is never deleted or overwritten: before anything else
// it is renamed aside as state.json.corrupt-<time>. backup "" then
// starts the shelf empty — the moved-aside original is the recovery
// copy; a named backup (one of RecoveryBackups) is copied in as the new
// state.json. A failed write puts the original back where it was, so the
// shelf never opens empty on a launch that told the user nothing changed;
// only when that rename fails too does the error name both files
// (RecoverFailure). Callers re-run Open afterwards.
func RecoverState(root, backup string) error {
	// Open 拒读的那份必须还在：恢复只对它做，不凭空生成 state.json。
	if _, err := os.Stat(filepath.Join(root, stateFileName)); err != nil {
		return err
	}
	var bdata []byte
	if backup != "" {
		var berr error
		if bdata, berr = os.ReadFile(filepath.Join(root, backup)); berr != nil {
			return berr
		}
		if _, perr := parseState(bdata); perr != nil {
			return fmt.Errorf("store: backup %s: %v", backup, perr)
		}
	}
	// 先把坏原件挪到一边：后面无论哪一步失败，它都完整保留。
	base := stateFileName + ".corrupt-" + time.Now().Format(corruptTimeLayout)
	aside := filepath.Join(root, base)
	for n := 2; ; n++ {
		if _, lerr := os.Lstat(aside); os.IsNotExist(lerr) {
			break
		}
		if n > 99 {
			return errors.New("store: no free corrupt-copy name left for today")
		}
		aside = filepath.Join(root, fmt.Sprintf("%s-%d", base, n))
	}
	original := filepath.Join(root, stateFileName)
	if err := os.Rename(original, aside); err != nil {
		return err
	}
	// 改名本身要落盘：掉电落在写入新文件之间时，挪走的原件不能只活在缓存里。
	_ = atomicfile.SyncDir(root)
	if backup == "" {
		return nil
	}
	if werr := writeStateFile(root, stateFileName, bdata, 0o600); werr != nil {
		// 新 state.json 没写成：把原件改名回原处，失败时说明里两个文件名
		// 都要写清，界面上不能再声称「文件保持原样」。
		if rerr := os.Rename(aside, original); rerr != nil {
			return &RecoverFailure{Aside: filepath.Base(aside), Err: fmt.Errorf(
				"store: writing the backup failed: %v; moving %s back to %s failed: %v",
				werr, filepath.Base(aside), stateFileName, rerr)}
		}
		_ = atomicfile.SyncDir(root)
		return fmt.Errorf("store: writing the backup failed, the original was moved back: %w", werr)
	}
	_ = atomicfile.SyncDir(root)
	return nil
}
