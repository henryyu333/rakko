//go:build windows

package aiusage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// launchCommand is how one Claude Code run is created: the executable
// CreateProcess is given, the exact command line it is given, and the
// environment entries that line reads. The three are not simply argv: a
// .cmd or .bat is read by cmd.exe, which parses the line — and the
// arguments of the script — by its own rules.
type launchCommand struct {
	exe     string
	cmdLine string
	env     []string
}

// claudeCommand builds the run for one Claude Code invocation. A copy
// installed by npm is a claude.cmd; Windows hands a batch file to the
// command interpreter with the arguments quoted the way a normal program
// wants them, which is not how cmd.exe splits them — a path with a space
// splits into two commands. The launch therefore names the interpreter and
// writes the line cmd reads. env is the child's environment; a batch launch
// adds its own entries to it.
func claudeCommand(path string, args, env []string) (*exec.Cmd, error) {
	launch, err := claudeLaunch(append([]string{path}, args...))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(launch.exe, args...)
	cmd.Env = launch.withEnv(env)
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
// AutoRun commands and /v:off keeps delayed expansion off; /s makes cmd take
// the quoted command as written instead of splitting it again.
//
// The script and its arguments are not written into the line at all: they
// travel in the environment, and the line only names each one as a %NAME%.
// cmd expands a name exactly once and does not scan the result again, so a
// %OS% or a %PATH% inside a path or an argument arrives as typed — written
// out, it would be rewritten, and C:\Tools\%OS%\claude.cmd is a legal
// install path.
func batchLaunch(argv []string) (launchCommand, error) {
	shell, err := cmdExe()
	if err != nil {
		return launchCommand{}, err
	}
	// A quote cannot be passed to a batch file: cmd has no escape for one
	// inside a quoted argument, and after an expansion an unquoted one would
	// end the argument and could start another command. The paths and
	// arguments this launcher passes are the app's own and contain none.
	for _, arg := range argv {
		if strings.Contains(arg, `"`) {
			return launchCommand{}, fmt.Errorf("aiusage: %q cannot be passed to %s", arg, filepath.Base(argv[0]))
		}
	}
	var line strings.Builder
	line.WriteString(quoteShell(shell))
	line.WriteString(" /d /v:off /s /c \"")
	env := make([]string, 0, len(argv))
	for i, arg := range argv {
		if i > 0 {
			line.WriteString(" ")
		}
		name := launchEnvName(i)
		line.WriteString(`"%` + name + `%"`)
		env = append(env, name+"="+arg)
	}
	line.WriteString("\"")
	return launchCommand{exe: shell, cmdLine: line.String(), env: env}, nil
}

// launchEnvName is the variable that carries one token of a batch launch.
// The name is the app's own, so a value can never be mistaken for one of the
// child's.
func launchEnvName(i int) string {
	return "RAKKO_LAUNCH_" + strconv.Itoa(i)
}

// withEnv returns env with the launch's entries set, replacing an entry of
// the same name so the child's block never names one twice.
func (l launchCommand) withEnv(env []string) []string {
	out := slices.Clone(env)
	for _, entry := range l.env {
		name, value, _ := strings.Cut(entry, "=")
		out = setEnv(out, name, value)
	}
	return out
}

// quoteShell quotes the interpreter's path on the command line. Nothing else
// is written into the line literally, so no other token can be expanded or
// rewritten by cmd's parsing.
func quoteShell(path string) string {
	return `"` + path + `"`
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
