//go:build windows

package agentnotify

// The shared secret behind the pipe's admission rule. The app writes a fresh
// 32-byte token into its data directory at every start; a client that can
// read it is, by that fact, the same user running at normal integrity or
// above — which is exactly the trust boundary. The file carries that rule in
// its own security descriptor from the moment it exists, temporary file
// included, so a low-integrity sandbox can neither read nor replace it.

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tokenTestHook is a test seam (see writeTokenFile).
var tokenTestHook func(stage string) error

const (
	// tokenName is the token file inside the data directory.
	tokenName = "agent-token"
	// tokenSize is the token's length in bytes.
	tokenSize = 32
)

// tokenSDDL is the token file's security descriptor: only this user has any
// access, and a low-integrity process can neither write (NW) nor read (NR)
// it. Windows defaults to no-write-up alone, so the explicit no-read-up is
// what keeps the token out of AppContainer and low-integrity processes.
func tokenSDDL(sid string) string {
	return "D:P(A;;FA;;;" + sid + ")S:(ML;;NRNW;;;ME)"
}

// tokenDACL is the token file's descriptor without the mandatory label, for
// the systems where creating an object with a SACL needs a privilege this
// process does not hold. The label is then applied to the open handle.
func tokenDACL(sid string) string { return "D:P(A;;FA;;;" + sid + ")" }

// writeToken generates this run's token and stores it in dataDir. The file
// is replaced atomically, so a client reads either the old token or the new
// one, never half of one.
func writeToken(dataDir string) ([]byte, error) {
	token := make([]byte, tokenSize)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(dataDir, tokenName+".new")
	final := filepath.Join(dataDir, tokenName)
	if err := writeTokenFile(tmp, token, sid); err != nil {
		return nil, err
	}
	if err := replaceTokenFile(final, tmp); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return token, nil
}

// procReplaceFileW swaps one file for another. MoveFileEx's replace refuses
// a destination that anybody has open (ACCESS_DENIED even when the reader
// shares delete), so a client reading the token while the app restarts could
// make Listen fail; ReplaceFileW is the API meant for replacing a live file.
var procReplaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func replaceTokenFile(final, tmp string) error {
	to, err := windows.UTF16PtrFromString(final)
	if err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	r1, _, callErr := procReplaceFileW.Call(
		uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)),
		0, 0, 0, 0)
	if r1 != 0 {
		return nil
	}
	if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
		// Nothing to replace (the first start, or a token deleted by hand):
		// a plain rename is all that is needed.
		if _, statErr := os.Lstat(final); errors.Is(statErr, os.ErrNotExist) {
			return windows.Rename(tmp, final)
		}
	}
	return callErr
}

func writeTokenFile(path string, token []byte, sid string) error {
	full, err := windows.SecurityDescriptorFromString(tokenSDDL(sid))
	if err != nil {
		return err
	}
	userDACL, err := windows.SecurityDescriptorFromString(tokenDACL(sid))
	if err != nil {
		return err
	}
	// CreateFile applies a descriptor's DACL but silently drops its SACL
	// unless the caller holds SeSecurityPrivilege, and a per-user install
	// does not. So the file is born with an empty DACL (nobody, not even
	// this process, can open it by name), the mandatory label is applied to
	// the open handle, and only then is the DACL opened to this user. The
	// label is therefore in place before any other process could read the
	// file — the token is never readable at low integrity, not even for a
	// moment.
	none, err := windows.SecurityDescriptorFromString("D:P")
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: none,
		InheritHandle:      0,
	}
	h, err := createTokenFile(path, sa)
	if err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// A leftover placeholder from a run that died before it could clean
		// up still carries the empty DACL, so opening it is refused; the
		// directory's own access rule lets this user delete it.
		if os.Remove(path) == nil {
			h, err = createTokenFile(path, sa)
		}
	}
	if err != nil {
		return err
	}
	// Every failure closes the handle before removing the file: DeleteFile
	// cannot remove a name whose handle is still open, and a leftover
	// empty-DACL placeholder would block every later start.
	fail := func(reason string, cause error) error {
		windows.CloseHandle(h)
		_ = os.Remove(path)
		if cause != nil {
			return fmt.Errorf("agentnotify: %s: %w", reason, cause)
		}
		return errors.New("agentnotify: " + reason)
	}
	// tokenTestHook is a test seam: it runs after the temporary file exists
	// and before it is labelled, so a test can check what a failure there
	// leaves behind. Production leaves it nil.
	if tokenTestHook != nil {
		if hookErr := tokenTestHook("created"); hookErr != nil {
			return fail("the token file could not be prepared", hookErr)
		}
	}
	sacl, _, err := full.SACL()
	if err != nil || sacl == nil {
		return fail("the token file could not be given its label", err)
	}
	if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); err != nil {
		return fail("the token file's integrity label", err)
	}
	dacl, _, err := userDACL.DACL()
	if err != nil || dacl == nil {
		return fail("the token file has no access rule", err)
	}
	if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fail("the token file's access rule", err)
	}
	var done uint32
	if err := windows.WriteFile(h, token, &done, nil); err != nil {
		return fail("the token file could not be written", err)
	}
	if int(done) != len(token) {
		return fail("the token file was written short", io.ErrShortWrite)
	}
	windows.CloseHandle(h)
	return nil
}

func createTokenFile(path string, sa *windows.SecurityAttributes) (windows.Handle, error) {
	namep, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	// WRITE_DAC/WRITE_OWNER are what applying the mandatory label to the
	// open handle needs; GENERIC_WRITE alone does not carry them.
	return windows.CreateFile(namep,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_DAC|windows.WRITE_OWNER,
		0, sa, windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

// openToken opens the token for reading while allowing a concurrent atomic
// replace: the writer renames over the file, and Windows refuses that rename
// unless the reader also shares delete access.
func openToken(path string) (windows.Handle, error) {
	namep, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(namep, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

// readToken loads this run's token. A process that cannot read the file
// (another account, or a low-integrity sandbox) is never admitted.
func readToken(dataDir string) ([]byte, error) {
	h, err := openToken(filepath.Join(dataDir, tokenName))
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, tokenSize+1)
	var done uint32
	if err := windows.ReadFile(h, buf, &done, nil); err != nil {
		return nil, err
	}
	if int(done) != tokenSize {
		return nil, errors.New("agentnotify: the token file is not a token")
	}
	return buf[:done], nil
}

// currentUserSID names this process's user, the one account the pipe and the
// token file admit.
func currentUserSID() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}
