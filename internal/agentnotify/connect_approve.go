package agentnotify

// Installing and removing the approval entries. They are a separate
// surface from completion notices: each carries --agent-approve, the
// removal functions only look for that marker, and switching one off
// leaves the other alone.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// ConnectApprove installs Rakko's approval entry for tool. exe is the app
// binary, dataDir its data directory (empty when it is the default).
func ConnectApprove(home, dataDir, exe string, tool Tool) error {
	s, ok := specFor(tool)
	if !ok || s.approve == nil {
		return fmt.Errorf("agentnotify: %s has no approval channel", tool)
	}
	ap := s.approve
	switch s.kind {
	case KindNamed:
		if ap.member == "" {
			return fmt.Errorf("agentnotify: %s has no approval member", tool)
		}
		return connectNamedMember(s, home, dataDir, ap.member, []string{ap.event}, false, ap.timeout,
			func(string) string { return ApproveCommand(exe, tool, dataDir) })
	case KindModule:
		return writeApproveModule(s, home, dataDir, exe)
	default:
		return connectApproveHooks(s, home, dataDir, exe)
	}
}

// DisconnectApprove removes every approval entry Rakko wrote for tool and
// touches no completion entry.
func DisconnectApprove(home, dataDir string, tool Tool) error {
	s, ok := specFor(tool)
	if !ok || s.approve == nil {
		return fmt.Errorf("agentnotify: %s has no approval channel", tool)
	}
	ap := s.approve
	switch s.kind {
	case KindNamed:
		return disconnectNamedMember(s, home, dataDir, ap.member)
	case KindModule:
		return removeApproveModule(s, home, dataDir)
	default:
		var first error
		for _, path := range s.candidates(home) {
			r := s
			r.bare = s.bareName != "" && filepath.Base(path) == s.bareName
			if err := disconnectHooksAt(r, path, dataDir, approveMarker(tool)); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
}

// StatusApprove reports whether Rakko's approval entry is installed.
func StatusApprove(home string, tool Tool) (State, string) {
	s, ok := specFor(tool)
	if !ok || s.approve == nil {
		return StateUnsupported, ""
	}
	ap := s.approve
	switch s.kind {
	case KindModule:
		path := ApproveConfigPath(home, tool)
		raw, existed, _, err := readConfig(path)
		if err != nil {
			return StateError, err.Error()
		}
		if !existed {
			return StateNotInjected, ""
		}
		if ownsApprove(raw, tool) {
			return StateInjected, ""
		}
		return StateError, path + " was not written by Rakko"
	case KindNamed:
		path := s.config(home)
		raw, existed, _, err := readConfig(path)
		if err != nil {
			return StateError, err.Error()
		}
		if !existed {
			return StateNotInjected, ""
		}
		doc, derr := newJSONDoc(raw)
		if derr != nil {
			return StateError, derr.Error()
		}
		if member, _ := doc.root.lookup(ap.member); member != nil {
			return StateInjected, ""
		}
		return StateNotInjected, ""
	default:
		s, path := s.at(home)
		raw, existed, _, err := readConfig(path)
		if err != nil {
			return StateError, err.Error()
		}
		if !existed {
			return StateNotInjected, ""
		}
		doc, derr := newJSONDoc(raw)
		if derr != nil {
			return StateError, derr.Error()
		}
		if hooksHasMarker(doc, s, approveMarker(tool)) {
			return StateInjected, ""
		}
		return StateNotInjected, ""
	}
}

// connectApproveHooks adds one approval entry to a hooks document. The file
// is Droid's bare hooks.json or a shared settings document, exactly like the
// completion entry.
func connectApproveHooks(s spec, home, dataDir, exe string) error {
	s, path := s.at(home)
	return updateFile(path, func(raw []byte, existed bool, mode os.FileMode) (filePlan, error) {
		doc, derr := newJSONDoc(raw)
		if derr != nil {
			return filePlan{}, fmt.Errorf("agentnotify: %s: %w", path, derr)
		}
		added, aerr := addHookCommand(doc, s, s.approve.event, s.approve.timeout,
			ApproveCommand(exe, s.tool, dataDir), approveMarker(s.tool))
		if aerr != nil {
			return filePlan{}, aerr
		}
		if !added {
			return filePlan{}, nil
		}
		m := os.FileMode(0o600)
		if existed {
			m = mode.Perm()
		}
		return filePlan{data: doc.bytes(), mode: m, changed: true, before: func() error {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			backed, err := backupOnce(path, existed)
			if err != nil {
				return err
			}
			recordOrigin(dataDir, path, originRecord{
				Created:  !existed,
				Blank:    existed && len(bytes.TrimSpace(raw)) == 0,
				BackedUp: backed,
			})
			return nil
		}}, nil
	})
}

// writeApproveModule writes the approval module file for a module tool.
func writeApproveModule(s spec, home, dataDir, exe string) error {
	path := ApproveConfigPath(home, s.tool)
	return updateFile(path, func(raw []byte, existed bool, _ os.FileMode) (filePlan, error) {
		if existed && !ownsApprove(raw, s.tool) {
			return filePlan{}, fmt.Errorf("agentnotify: %s exists and was not written by Rakko; left alone", path)
		}
		body := renderApproveModule(s.approve.module, s.tool, exe, dataDir)
		return filePlan{data: []byte(body), mode: 0o644, changed: true, before: func() error {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			recordOrigin(dataDir, path, originRecord{Created: true})
			return nil
		}}, nil
	})
}

// removeApproveModule deletes Rakko's approval module file and nothing else.
func removeApproveModule(s spec, home, dataDir string) error {
	path := ApproveConfigPath(home, s.tool)
	return updateFile(path, func(raw []byte, existed bool, _ os.FileMode) (filePlan, error) {
		if !existed {
			return filePlan{after: func() { forgetOrigin(dataDir, path) }}, nil
		}
		if !ownsApprove(raw, s.tool) {
			return filePlan{}, nil
		}
		return filePlan{remove: true, changed: true, after: func() { forgetOrigin(dataDir, path) }}, nil
	})
}

// ownsApprove reports whether a module file is Rakko's approval file.
func ownsApprove(raw []byte, tool Tool) bool {
	return bytes.Contains(raw, []byte(approveModuleMarker)) || bytes.Contains(raw, []byte(approveMarker(tool)))
}
