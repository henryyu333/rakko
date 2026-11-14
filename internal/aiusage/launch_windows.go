//go:build windows

package aiusage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// launchCommand is how one Claude Code run is created: the executable
// CreateProcess is given and the exact command line it is given. The two
// are not simply argv: a .cmd or .bat is read by cmd.exe, which parses the
// line — and the arguments of the script — by its own rules.
type launchCommand struct {
	exe     string
	cmdLine string
}

// claudeCommand builds the run for one Claude Code invocation. A copy
// installed by npm is a claude.cmd; Windows hands a batch file to the
// command interpreter with the arguments quoted the way a normal program
// wants them, which is not how cmd.exe splits them — a path with a space
// splits into two commands. The launch therefore names the interpreter and
// writes the line cmd reads.
func claudeCommand(path string, args []string) (*exec.Cmd, error) {
	launch, err := claudeLaunch(append([]string{path}, args...))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(launch.exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: launch.cmdLine}
	return cmd, nil
}

// claudeLaunch builds the launch for argv, whose first element is the
// resolved program. Anything Windows can load is started from the argv it
// would get anyway; a batch file is run under cmd.exe with the quoting cmd
// itself understands.
func claudeLaunch(argv []string) (launchCommand, error) {
	if len(argv) == 0 || argv[0] == "" {
		return launchCommand{}, fmt.Errorf("aiusage: no program to start")
	}
	switch strings.ToLower(filepath.Ext(argv[0])) {
	case ".cmd", ".bat":
		return batchLaunch(argv)
	}
	return launchCommand{exe: argv[0], cmdLine: windows.ComposeCommandLine(argv)}, nil
}

// batchLaunch wraps a batch file in the command interpreter. /d skips the
// AutoRun commands, and /s makes cmd take the quoted command as written
// instead of splitting it again: the whole line is one quoted command whose
// first token is the quoted script path.
func batchLaunch(argv []string) (launchCommand, error) {
	shell, err := cmdExe()
	if err != nil {
		return launchCommand{}, err
	}
	// A quote cannot be passed to a batch file: cmd has no escape for one
	// inside a quoted argument, and an unquoted one would end the argument
	// and could start another command. The paths and arguments this
	// launcher passes are the app's own and contain none.
	for _, arg := range argv {
		if strings.Contains(arg, `"`) {
			return launchCommand{}, fmt.Errorf("aiusage: %q cannot be passed to %s", arg, filepath.Base(argv[0]))
		}
	}
	var line strings.Builder
	line.WriteString(quoteCmd(shell))
	line.WriteString(" /d /s /c \"")
	line.WriteString(quoteCmd(argv[0]))
	for _, arg := range argv[1:] {
		line.WriteString(" ")
		line.WriteString(quoteCmd(arg))
	}
	line.WriteString("\"")
	return launchCommand{exe: shell, cmdLine: line.String()}, nil
}

// quoteCmd quotes one token for cmd.exe. Inside double quotes cmd leaves
// spaces, &, |, <, >, ^ and parentheses alone, so a path with a space and
// arguments with those characters arrive as typed. A %NAME% is expanded by
// cmd whatever the quotes; the arguments this launcher passes never hold
// one.
func quoteCmd(arg string) string {
	return `"` + arg + `"`
}

// cmdExe is the command interpreter a batch file runs under.
func cmdExe() (string, error) {
	if comspec := os.Getenv("ComSpec"); comspec != "" {
		return comspec, nil
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	path := filepath.Join(root, "System32", "cmd.exe")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("aiusage: finding the command interpreter: %w", err)
	}
	return path, nil
}
