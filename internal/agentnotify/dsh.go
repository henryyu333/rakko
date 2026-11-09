package agentnotify

// DeepSeek Harness (dsh) composes each profile from patch layers: the
// bundles named in the profile's package.json, the profile's own
// cordis.patch.yml, then $DSH_HOME/cordis.patch.yml, which every profile
// reads. Rakko owns one module file per surface and one insertion entry
// for it in that home-level layer, so the user's profile directories are
// never touched.
//
// The modules use dsh's own seams: session events for completion and the
// approval/request waterfall for permission asks. That waterfall runs only
// when dsh itself was going to prompt, and Rakko's answer is a one-shot
// grant, so this is not a per-tool-call interceptor.
//
// Written against @deepseek-ai/dsh 0.2.2-rc.1 (Cordis plugin tree).

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dshEntryCompletion and dshEntryApproval are the two loader entry ids
// Rakko owns. They are also the marker words of the module files.
const (
	dshEntryCompletion = moduleMarker
	dshEntryApproval   = approveModuleMarker
)

// dshHome is the directory dsh keeps profiles and sessions in: $DSH_HOME
// when the environment names one, else ~/.dsh. dsh's own home-path
// resolver reads the same variable.
func dshHome(home string) string {
	dir := os.Getenv("DSH_HOME")
	if dir == "" {
		return join(home, ".dsh")
	}
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") {
		return join(home, dir[2:])
	}
	return dir
}

// dshPatchPath is the home-level patch layer applied to every dsh profile.
func dshPatchPath(home string) string {
	return join(dshHome(home), "cordis.patch.yml")
}

// dshModulePath is one of Rakko's two module files.
func dshModulePath(home, entry string) string {
	return join(dshHome(home), "rakko", entry+".mjs")
}

// dshPatchMarker starts the block Rakko appends for one entry. A later
// disconnect finds the block by it.
func dshPatchMarker(entry string) string {
	return "# " + entry + ": written by Rakko."
}

// dshPatchBlock is the patch entry appended to the layer: a top-level list
// item that inserts one loader row. The module is named by absolute path,
// which the loader imports directly.
func dshPatchBlock(entry, module string) string {
	return dshPatchMarker(entry) + " Removed when its switch is off.\n" +
		"- insert:\n" +
		"    - id: " + entry + "\n" +
		"      name: " + yamlQuote(module) + "\n"
}

// yamlQuote writes a single-quoted YAML scalar: a quote doubles.
func yamlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// firstYAMLLine returns the trimmed first line that carries a YAML node,
// skipping blank lines, comments and a document-start marker.
func firstYAMLLine(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "---" {
			continue
		}
		return line
	}
	return ""
}

// emptyFlowOffset returns the offset of the "[]" that is the file's only
// YAML node, or -1. Rakko replaces that token with its block and remembers
// to put it back.
func emptyFlowOffset(raw []byte) int {
	off := 0
	for off < len(raw) {
		end := bytes.IndexByte(raw[off:], '\n')
		if end < 0 {
			end = len(raw) - off
		}
		line := strings.TrimSpace(string(raw[off : off+end]))
		if line != "" && !strings.HasPrefix(line, "#") && line != "---" {
			if line == "[]" {
				return off + bytes.Index(raw[off:off+end], []byte("[]"))
			}
			return -1
		}
		off += end + 1
	}
	return -1
}

// addDSHPatch appends Rakko's entry unless it is already there. The file
// must be absent, hold comments only, begin with a top-level list item, or
// hold the empty flow sequence; anything else is left alone with an error
// rather than rewritten by a guess.
func addDSHPatch(raw []byte, entry, module string) (out []byte, changed, noFinalNewline, flowEmpty bool, err error) {
	if bytes.Contains(raw, []byte(dshPatchMarker(entry))) {
		return raw, false, false, false, nil
	}
	if off := emptyFlowOffset(raw); off >= 0 {
		// The block ends with a newline, so it replaces the token's whole
		// line: removing it puts "[]\n" back, or "[]" when the token ended
		// the file without one.
		tail := raw[off+2:]
		if nl := bytes.IndexByte(tail, '\n'); nl >= 0 {
			tail = tail[nl+1:]
		} else {
			tail, noFinalNewline = nil, true
		}
		res := make([]byte, 0, len(raw)+len(dshPatchBlock(entry, module)))
		res = append(res, raw[:off]...)
		res = append(res, dshPatchBlock(entry, module)...)
		res = append(res, tail...)
		return res, true, noFinalNewline, true, nil
	}
	if line := firstYAMLLine(raw); line != "" &&
		!strings.HasPrefix(line, "- ") && line != "-" {
		return nil, false, false, false, errors.New("not a top-level YAML patch list")
	}
	res := make([]byte, 0, len(raw)+len(dshPatchBlock(entry, module))+1)
	res = append(res, raw...)
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		res = append(res, '\n')
		noFinalNewline = true
	}
	res = append(res, dshPatchBlock(entry, module)...)
	return res, true, noFinalNewline, false, nil
}

// removeDSHPatch removes Rakko's entry and nothing else. The block runs
// from its marker to the next top-level list item.
func removeDSHPatch(raw []byte, entry string, rec originRecord) (out []byte, changed bool, err error) {
	marker := []byte(dshPatchMarker(entry))
	start := bytes.Index(raw, marker)
	if start < 0 {
		return raw, false, nil
	}
	if start > 0 && raw[start-1] != '\n' {
		return nil, false, errors.New("Rakko's patch marker is not at the start of a line")
	}
	end := len(raw)
	// Our block is the marker line, a top-level "- insert:" and the
	// indented rows under it. The next top-level line, blank line, or the
	// file's end terminates it. A file edited so the marker no longer
	// starts a block loses only the marker.
	insert := nextLine(raw, start)
	if lineText(raw, insert) != "- insert:" {
		end = insert
	} else {
		pos := nextLine(raw, insert)
		for pos < len(raw) {
			line := lineText(raw, pos)
			if len(line) == 0 || (line[0] != ' ' && line[0] != '\t') {
				end = pos
				break
			}
			pos = nextLine(raw, pos)
		}
	}
	rest := make([]byte, 0, len(raw)-(end-start))
	rest = append(rest, raw[:start]...)
	rest = append(rest, raw[end:]...)
	res := make([]byte, 0, len(rest)+2)
	res = append(res, rest[:start]...)
	if rec.FlowEmpty && !dshHasPatchBlock(rest) {
		// The empty flow sequence comes back only once Rakko's last block is
		// gone. With the other surface still installed the layer must stay a
		// list, and a second "[]" beside it would not be YAML. The block took
		// the token's line ending with it, so the newline comes back too; a
		// token that ended the file has none, which NoFinalNewline strips.
		res = append(res, "[]\n"...)
	}
	res = append(res, rest[start:]...)
	if rec.NoFinalNewline && end == len(raw) && len(res) > 0 && res[len(res)-1] == '\n' {
		res = res[:len(res)-1]
	}
	return res, true, nil
}

// dshHasPatchBlock reports whether raw still holds one of Rakko's patch
// entries.
func dshHasPatchBlock(raw []byte) bool {
	return bytes.Contains(raw, []byte(dshPatchMarker(dshEntryCompletion))) ||
		bytes.Contains(raw, []byte(dshPatchMarker(dshEntryApproval)))
}

// lineText returns the line at start without its newline.
func lineText(raw []byte, start int) string {
	if start >= len(raw) {
		return ""
	}
	line := raw[start:]
	if nl := bytes.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	return string(line)
}

// nextLine returns the offset after the newline that ends the line at
// start, or len(raw) when there is none.
func nextLine(raw []byte, start int) int {
	if start >= len(raw) {
		return len(raw)
	}
	nl := bytes.IndexByte(raw[start:], '\n')
	if nl < 0 {
		return len(raw)
	}
	return start + nl + 1
}

// dshOnlyComments reports whether text holds no YAML node.
func dshOnlyComments(text []byte) bool {
	return firstYAMLLine(text) == ""
}

// --- install / remove ---

func writePatchModule(path, tmpl string, tool Tool, exe, dataDir string, approve bool) error {
	owns := ownsModule
	if approve {
		owns = ownsApprove
	}
	return updateFile(path, func(raw []byte, existed bool, _ os.FileMode) (filePlan, error) {
		if existed && !owns(raw, tool) {
			return filePlan{}, fmt.Errorf("agentnotify: %s exists and was not written by Rakko; left alone", path)
		}
		var body string
		if approve {
			body = renderApproveModule(tmpl, tool, exe, dataDir)
		} else {
			body = renderModule(tmpl, tool, exe, dataDir)
		}
		return filePlan{data: []byte(body), mode: 0o644, changed: true, before: func() error {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			recordOrigin(dataDir, path, originRecord{Created: true})
			return nil
		}}, nil
	})
}

// connectDSHPatch writes one module and its insertion entry. The layer is
// checked first, so an unparseable file is left alone without leaving a
// module behind; the module goes next, and a failed patch write leaves only
// an unreferenced file.
func connectDSHPatch(s spec, home, dataDir, exe, entry, path, tmpl string, approve bool) error {
	patch := s.patch(home)
	if raw, _, _, err := readConfig(patch); err != nil {
		return err
	} else if _, _, _, _, aerr := addDSHPatch(raw, entry, path); aerr != nil {
		return fmt.Errorf("agentnotify: %s: %w", patch, aerr)
	}
	if err := writePatchModule(path, tmpl, s.tool, exe, dataDir, approve); err != nil {
		return err
	}
	return updateFile(patch, func(raw []byte, existed bool, mode os.FileMode) (filePlan, error) {
		out, changed, noFinalNewline, flowEmpty, err := addDSHPatch(raw, entry, path)
		if err != nil {
			return filePlan{}, fmt.Errorf("agentnotify: %s: %w", patch, err)
		}
		if !changed {
			return filePlan{}, nil
		}
		m := os.FileMode(0o600)
		if existed {
			m = mode.Perm()
		}
		return filePlan{data: out, mode: m, changed: true, before: func() error {
			if err := os.MkdirAll(filepath.Dir(patch), 0o700); err != nil {
				return err
			}
			// A layer holding only Rakko's blocks has no user bytes to keep:
			// the first surface created it, and the second must not back up
			// Rakko's own file.
			backed, err := backupOnce(patch, existed && !dshOnlyRakko(raw))
			if err != nil {
				return err
			}
			// The first surface's write records what the user's file looked
			// like; the second must not forget it, because switching off
			// restores the original bytes.
			rec := readOrigin(dataDir, patch)
			if !existed || rec == (originRecord{}) {
				rec = originRecord{
					Created:  !existed,
					Blank:    existed && len(bytes.TrimSpace(raw)) == 0,
					BackedUp: backed,
				}
			}
			rec.NoFinalNewline = rec.NoFinalNewline || noFinalNewline
			rec.FlowEmpty = rec.FlowEmpty || flowEmpty
			recordOrigin(dataDir, patch, rec)
			return nil
		}}, nil
	})
}

// disconnectDSHPatch removes one entry and its module. A layer Rakko
// created goes away once nothing is left in it; a user's layer keeps every
// other byte.
func disconnectDSHPatch(s spec, home, dataDir, entry, path string, approve bool) error {
	patch := s.patch(home)
	err := updateFile(patch, func(raw []byte, existed bool, mode os.FileMode) (filePlan, error) {
		if !existed {
			return filePlan{after: func() { forgetOrigin(dataDir, patch) }}, nil
		}
		rec := readOrigin(dataDir, patch)
		out, changed, rerr := removeDSHPatch(raw, entry, rec)
		if rerr != nil {
			return filePlan{}, fmt.Errorf("agentnotify: %s: %w", patch, rerr)
		}
		if !changed {
			return filePlan{}, nil
		}
		switch {
		case rec.Created && dshOnlyComments(out):
			return filePlan{remove: true, changed: true, after: func() {
				forgetOrigin(dataDir, patch)
			}}, nil
		case len(bytes.TrimSpace(out)) == 0 && rec.Blank:
			out = nil
		}
		plan := filePlan{data: out, mode: mode.Perm(), changed: true}
		if !dshHasPatchBlock(out) {
			// Both surfaces are gone: the file is the user's again, and the
			// record of its original shape has nothing left to restore.
			plan.after = func() { forgetOrigin(dataDir, patch) }
		}
		return plan, nil
	})
	if err != nil {
		return err
	}
	owns := ownsModule
	if approve {
		owns = ownsApprove
	}
	err = updateFile(path, func(raw []byte, existed bool, _ os.FileMode) (filePlan, error) {
		if !existed {
			return filePlan{after: func() { forgetOrigin(dataDir, path) }}, nil
		}
		if !owns(raw, s.tool) {
			return filePlan{}, nil
		}
		return filePlan{remove: true, changed: true, after: func() { forgetOrigin(dataDir, path) }}, nil
	})
	if err == nil {
		// The directory is Rakko's own; remove it when this was its last
		// file, and leave it when the other surface still lives there.
		_ = os.Remove(filepath.Dir(path))
	}
	return err
}

// dshOnlyRakko reports whether a layer holds nothing but Rakko's two
// blocks and comments.
func dshOnlyRakko(raw []byte) bool {
	rest := raw
	for _, entry := range []string{dshEntryCompletion, dshEntryApproval} {
		out, changed, err := removeDSHPatch(rest, entry, originRecord{})
		if err != nil {
			return false
		}
		if changed {
			rest = out
		}
	}
	return dshOnlyComments(rest)
}

// statusDSHPatch reports one surface: both the module and its entry must
// be present.
func statusDSHPatch(s spec, home, entry, path string) (State, string) {
	raw, existed, _, err := readConfig(s.patch(home))
	if err != nil {
		return StateError, err.Error()
	}
	entryThere := existed && bytes.Contains(raw, []byte(dshPatchMarker(entry)))
	moduleThere := exists(path)
	switch {
	case entryThere && moduleThere:
		return StateInjected, ""
	case entryThere:
		return StateError, path + " is missing; turn the switch off and on again"
	case moduleThere:
		return StateNotInjected, ""
	}
	return StateNotInjected, ""
}
