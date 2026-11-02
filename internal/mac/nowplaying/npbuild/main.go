// Command npbuild compiles the Rakko now-playing helper bundle from the
// C source next to this file and places it where `mygo build` picks it
// up: resources/darwin-arm64/RakkoNowPlaying.bundle. `mygo build` signs
// the bundle as part of the app (mygo signs Mach-O code inside
// Contents/Resources).
//
// Run it from the repository root (go generate does):
//
//	go generate ./...
//
// The helper is a plain C bundle, not a program: the ShelfU app loads it
// into /usr/bin/perl, because since macOS 15.4 MediaRemote only answers
// Apple-signed callers.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, "npbuild:", err)
		os.Exit(1)
	}
}

// repoRoot walks up from this file's directory to the module root (the
// one holding go.mod), so the command works from any working directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate this source file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

func build() error {
	clang, err := exec.LookPath("clang")
	if err != nil {
		return fmt.Errorf("clang not found (install the Xcode command line tools): %w", err)
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	src := filepath.Join(root, "internal", "mac", "nowplaying", "helper.c")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("helper source: %w", err)
	}
	out := filepath.Join(root, "resources", "darwin-arm64", "RakkoNowPlaying.bundle")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	// -bundle: a loadable Mach-O bundle; -fblocks: MediaRemote's callbacks;
	// -dead_strip: no unused code in the shipped file.
	cmd := exec.Command(clang, "-bundle", "-fblocks", "-O2", "-Wall", "-Wextra",
		"-dead_strip", "-o", out, src,
		"-framework", "CoreFoundation", "-framework", "Foundation")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clang: %w", err)
	}
	// Ad-hoc signature so the file is valid to load and to re-sign.
	sign := exec.Command("codesign", "--force", "-s", "-", out)
	sign.Stdout = os.Stdout
	sign.Stderr = os.Stderr
	if err := sign.Run(); err != nil {
		return fmt.Errorf("codesign: %w", err)
	}
	fmt.Println("built", out)
	return nil
}
