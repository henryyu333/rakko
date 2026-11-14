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
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	to, err := windows.UTF16PtrFromString(final)
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return token, nil
}

func writeTokenFile(path string, token []byte, sid string) error {
	full, err := windows.SecurityDescriptorFromString(tokenSDDL(sid))
	if err != nil {
		return err
	}
	dacl, err := windows.SecurityDescriptorFromString(tokenDACL(sid))
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: full,
		InheritHandle:      0,
	}
	h, err := createTokenFile(path, sa)
	labeled := err == nil
	if err != nil && errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
		sa.SecurityDescriptor = dacl
		h, err = createTokenFile(path, sa)
		if err == nil {
			labeled = false
		}
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if !labeled {
		sacl, present, serr := full.SACL()
		if serr != nil || !present {
			_ = os.Remove(path)
			return errors.New("agentnotify: the token file could not be given its label")
		}
		if serr := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
			windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); serr != nil {
			_ = os.Remove(path)
			return serr
		}
	}
	var done uint32
	if err := windows.WriteFile(h, token, &done, nil); err != nil {
		_ = os.Remove(path)
		return err
	}
	if int(done) != len(token) {
		_ = os.Remove(path)
		return io.ErrShortWrite
	}
	return nil
}

func createTokenFile(path string, sa *windows.SecurityAttributes) (windows.Handle, error) {
	namep, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(namep, windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, sa, windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

// readToken loads this run's token. A process that cannot read the file
// (another account, or a low-integrity sandbox) is never admitted.
func readToken(dataDir string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, tokenName))
	if err != nil {
		return nil, err
	}
	if len(raw) != tokenSize {
		return nil, errors.New("agentnotify: the token file is not a token")
	}
	return raw, nil
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
