package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"rakko/internal/atomicfile"
)

// ErrStateUnreadable marks a state.json Open refused to read: it cannot
// be parsed, it carries a version this build does not understand, or it
// holds an item kind a newer Rakko wrote. The file is intact on disk and
// Open never overwrites it; every way out of it is the user's call, and
// RecoverState is the one this package offers.
var ErrStateUnreadable = errors.New("store: state file is unreadable")

// corruptTimeLayout names the moved-aside unreadable original:
// state.json.corrupt-<20060102-150405>. The bad file is never deleted
// and never overwritten; whatever else the user picks, it stays.
const corruptTimeLayout = "20060102-150405"

// parseState validates the bytes of a state.json and returns its parsed
// contents. Open and the recovery paths agree through it on what
// "readable" means: the JSON parses, the version is one this build
// understands, and every item is a kind this build knows — a state a
// newer Rakko wrote must be refused, not silently trimmed.
func parseState(data []byte) (stateFile, error) {
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return sf, fmt.Errorf("%w: parsing %s: %v", ErrStateUnreadable, stateFileName, err)
	}
	if sf.Version != stateVersion {
		return sf, fmt.Errorf("%w: %s holds state version %d, this Rakko understands %d",
			ErrStateUnreadable, stateFileName, sf.Version, stateVersion)
	}
	for _, it := range sf.Items {
		switch it.Kind {
		case KindText, KindImage, KindFile:
		default:
			return sf, fmt.Errorf("%w: %s holds a %q item a newer Rakko wrote; upgrade rather than let this one erase it",
				ErrStateUnreadable, stateFileName, it.Kind)
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

// RecoverState is the user's way out of an unreadable state.json. The
// corrupt original is never deleted or overwritten: before anything else
// it is renamed aside as state.json.corrupt-<time>. backup "" then
// starts the shelf empty — the moved-aside original is the recovery
// copy; a named backup (one of RecoveryBackups) is copied in as the new
// state.json. Callers re-run Open afterwards; whatever Open finds then,
// the bad file it refused stayed put.
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
	if err := os.Rename(filepath.Join(root, stateFileName), aside); err != nil {
		return err
	}
	if backup == "" {
		return nil
	}
	return atomicfile.WriteFile(root, stateFileName, bdata, 0o600)
}
