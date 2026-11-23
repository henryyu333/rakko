package agentnotify

// Installing and removing the approval entries. They are a separate
// surface from completion notices: each carries --agent-approve, the
// removal functions only look for that marker, and switching one off
// leaves the other alone.

import (
	"bytes"
	"fmt"
	"os"
)

// ConnectApprove installs Rakko's approval entry for tool. exe is the app
// binary, dataDir its data directory (empty when it is the default).
func ConnectApprove(home, dataDir, exe string, tool Tool) error {
	s, ok := specFor(tool)
	if !ok || s.approve == nil {
		return fmt.Errorf("agentnotify: %s has no approval channel", tool)
	}
	ap := s.approve
	existedShim := shimExisted(dataDir, hookApproval, tool)
	if err := prepareHookShim(hookApproval, s, dataDir, exe); err != nil {
		return err
	}
	var err error
	switch s.kind {
	case KindHermes:
		err = connectHermesApprove(s, home, dataDir, exe)
	case KindNamed:
		if ap.member == "" {
			err = fmt.Errorf("agentnotify: %s has no approval member", tool)
			break
		}
		err = connectNamedMember(s, home, dataDir, ap.member, []string{ap.event}, ap.timeout, hookApproval,
			func(string) string { return approveEntryFor(exe, tool, dataDir).command() })
	case KindModule:
		err = writeApproveModule(s, home, dataDir, exe)
	case KindPatch:
		err = connectDSHPatch(s, home, dataDir, exe, dshEntryApproval,
			ApproveConfigPath(home, tool), ap.module, true)
	default:
		err = connectApproveHooks(s, home, dataDir, exe)
	}
	if err != nil && !existedShim {
		_ = removeShim(dataDir, hookApproval, tool)
	}
	return err
}

// DisconnectApprove removes every approval entry Rakko wrote for tool and
// touches no completion entry.
func DisconnectApprove(home, dataDir string, tool Tool) error {
	s, ok := specFor(tool)
	if !ok || s.approve == nil {
		return fmt.Errorf("agentnotify: %s has no approval channel", tool)
	}
	ap := s.approve
	var err error
	switch s.kind {
	case KindHermes:
		err = disconnectHermesApprove(s, home, dataDir)
	case KindNamed:
		err = disconnectNamedMember(s, home, dataDir, ap.member)
	case KindModule:
		err = removeModuleFile(ApproveConfigPath(home, tool), hookApproval, s.tool, dataDir)
	case KindPatch:
		err = disconnectDSHPatch(s, home, dataDir, dshEntryApproval,
			ApproveConfigPath(home, tool), true)
	default:
		err = disconnectHooks(s, home, dataDir, hookApproval)
	}
	if err != nil {
		return err
	}
	return removeShim(dataDir, hookApproval, tool)
}

// StatusApprove reports whether Rakko's approval entry is installed.
func StatusApprove(home, dataDir string, tool Tool) (State, string) {
	return statusFor(home, dataDir, tool, hookApproval)
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
			approveEntryFor(exe, s.tool, dataDir), hookApproval, dataDir)
		if aerr != nil {
			return filePlan{}, aerr
		}
		if !added {
			return filePlan{}, nil
		}
		return hookFilePlan(path, dataDir, raw, doc.bytes(), existed, mode), nil
	})
}

// writeApproveModule writes the approval module file for a module tool.
func writeApproveModule(s spec, home, dataDir, exe string) error {
	body := renderApproveModule(moduleSource(s, hookApproval), s.tool, exe, dataDir)
	return writeModuleFile(ApproveConfigPath(home, s.tool), body, hookApproval, s.tool, dataDir)
}

// ownsApprove reports whether a module file is Rakko's approval file.
func ownsApprove(raw []byte, tool Tool, dataDir string) bool {
	return bytes.Contains(raw, []byte(approveModuleMarker)) || carriesKind(raw, tool, hookApproval, dataDir)
}
