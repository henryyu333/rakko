//go:build darwin

package main

// TestUpdateEndToEnd is the local, GitHub-free check of the whole update
// path. It builds this package twice as a minimal Rakko.app in a temp dir:
// the installed copy (version 0.0.1) and the update payload (9.9.9), serves
// a manifest and a signed archive from a loopback server (mygo's httpGet
// allows plain HTTP to loopback, which is why the test needs no TLS), and
// launches the old copy with a temp --data-dir and SHELF_UPDATE_SELFTEST=1 —
// the hook in checkedUpdate that takes the exact path the 安装更新 menu
// item takes, so the test sees the real download, signature check and
// bundle swap. The release key is not involved: a throwaway key pair is
// baked in with -ldflags. Run with SHELF_UPDATE_E2E=1; the launched copy
// briefly registers the ⌃⌥ global shortcuts and shows a tray icon.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testUpdateKey is generated per run; it signs the fake archive and is
// baked into both builds as the public key.
type testApp struct {
	exe string // the running binary, inside app
	app string // the .app bundle path
}

// buildTestApp compiles the shelf binary and wraps it in the minimal
// bundle App.Version and the updater need: Rakko.app/Contents/MacOS/Rakko
// with an Info.plist holding the version.
func buildTestApp(t *testing.T, dir, version, feed, pub string) testApp {
	t.Helper()
	root := filepath.Join(dir, "Rakko.app")
	exe := filepath.Join(root, "Contents", "MacOS", "Rakko")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	ld := fmt.Sprintf("-X github.com/egoist/mygo.packageUpdateFeed=%s "+
		"-X github.com/egoist/mygo.packageUpdateKey=%s "+
		"-X github.com/egoist/mygo.production=1 "+
		"-X github.com/egoist/mygo.packageVersion=%s", feed, pub, version)
	out, err := exec.Command("go", "build", "-o", exe, "-ldflags", ld, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build (%s): %v\n%s", version, err, out)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>Rakko</string>
<key>CFBundleIdentifier</key><string>local.y3.rakko.e2e</string>
<key>CFBundleName</key><string>Rakko</string>
<key>CFBundleShortVersionString</key><string>%s</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
</dict></plist>`, version)
	if err := os.WriteFile(filepath.Join(root, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unregisterBundle(t, root) })
	return testApp{exe: exe, app: root}
}

// unregisterBundle drops a test bundle from LaunchServices. A .app launched
// from a temp dir is registered there, and a deleted one would keep showing
// up as an "Open With" entry long after the test.
func unregisterBundle(t *testing.T, app string) {
	t.Helper()
	const lsregister = "/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister"
	if _, err := os.Stat(lsregister); err != nil {
		return // registration only happens where LaunchServices lives
	}
	if out, err := exec.Command(lsregister, "-u", app).CombinedOutput(); err != nil {
		t.Logf("lsregister -u %s: %v: %s", app, err, out)
	}
}

// tarGzApp archives the bundle the way update.WriteArchive does: the .app
// as the single root entry, permissions preserved.
func tarGzApp(t *testing.T, app string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	parent := filepath.Dir(app)
	err := filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(parent, p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// syncLog collects the child's combined output while exec's copy goroutines
// write into it and the test reads it.
type syncLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestUpdateEndToEnd(t *testing.T) {
	if os.Getenv("SHELF_UPDATE_E2E") != "1" {
		t.Skip("set SHELF_UPDATE_E2E=1 to run the update end-to-end test (launches a real Rakko instance)")
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	// The feed's URL contains the server's random port, so the server must
	// be up before either binary is built; handlers close over these.
	// atomic.Value: handlers run on the server's goroutines while the test
	// fills the feed after the builds.
	var feedManifest, feedArchive atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/update-darwin-arm64.json":
			if v := feedManifest.Load(); v != nil {
				w.Write(v.([]byte))
				return
			}
		case "/shelf-9.9.9-darwin-arm64.tar.gz":
			if v := feedArchive.Load(); v != nil {
				w.Write(v.([]byte))
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	feed := srv.URL + "/update-darwin-arm64.json"

	work := t.TempDir()
	newApp := buildTestApp(t, filepath.Join(work, "payload"), "9.9.9", feed, pubB64)
	oldApp := buildTestApp(t, filepath.Join(work, "installed"), "0.0.1", feed, pubB64)

	archive := tarGzApp(t, newApp.app)
	sum := sha256.Sum256(archive)
	manifest, err := json.Marshal(map[string]any{
		"version":   "9.9.9",
		"date":      time.Now().UTC().Format(time.RFC3339),
		"url":       srv.URL + "/shelf-9.9.9-darwin-arm64.tar.gz",
		"size":      int64(len(archive)),
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:])),
	})
	if err != nil {
		t.Fatal(err)
	}
	feedArchive.Store(archive)
	feedManifest.Store(manifest)

	dataDir := filepath.Join(work, "data")
	// syncLog guards the child's output: the exec copy goroutines write to
	// it while the test reads it.
	var logBuf syncLog
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, oldApp.exe, "--data-dir", dataDir)
	cmd.Env = append(os.Environ(), "SHELF_UPDATE_SELFTEST=1")
	cmd.Stdout, cmd.Stderr = &logBuf, &logBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait reaps the app as soon as it exits. That matters: mygo's update
	// helper waits for the process to disappear, and an unreaped child
	// stays visible to kill(2) as a zombie, so the helper would wait for
	// the test's own Wait (a deadlock production never sees, where launchd
	// reaps the app).
	waited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waited)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		<-waited
		// A run that fails after staging still lets the helper swap the
		// bundle in and open the new version once this process is gone.
		// Leave no Rakko instance behind.
		cleanup := time.Now().Add(2 * time.Second)
		for time.Now().Before(cleanup) {
			out, _ := exec.Command("pgrep", "-f", oldApp.exe).Output()
			pids := strings.Fields(string(out))
			if len(pids) == 0 {
				return
			}
			for _, f := range pids {
				_ = exec.Command("kill", f).Run()
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	want := sha256Of(t, newApp.exe)
	// mygo v0.7.0 stages the update and installs it once the app exits, so
	// this follows the restart path the hook takes: the running copy must
	// exit after installing, and the helper then swaps the bundle in.
	// Replacement is still required within 90 seconds of the launch.
	deadline := time.Now().Add(90 * time.Second)
	replaced := false
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(oldApp.exe); err == nil && fmt.Sprintf("%x", sha256.Sum256(b)) == want {
			replaced = true
			break
		}
		select {
		case <-waited:
			if !strings.Contains(logBuf.String(), "已安装") {
				t.Fatalf("app exited before updating:\n%s", logBuf.String())
			}
		default:
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !replaced {
		t.Fatalf("the installed app was not replaced within 90s; log:\n%s", logBuf.String())
	}
	logs := logBuf.String()
	for _, marker := range []string{"发现新版本 v9.9.9", "已安装"} {
		if !strings.Contains(logs, marker) {
			t.Errorf("app log lacks %q; log:\n%s", marker, logs)
		}
	}
	// The swapped bundle is complete: its plist must carry the new version.
	if plist, err := os.ReadFile(filepath.Join(oldApp.app, "Contents", "Info.plist")); err != nil || !strings.Contains(string(plist), "9.9.9") {
		t.Errorf("installed Info.plist not updated: %v", err)
	}
	if olds, _ := filepath.Glob(filepath.Join(filepath.Dir(oldApp.app), ".*.old-*")); len(olds) > 0 {
		t.Errorf("swap left leftovers: %v", olds)
	}
	// 重新启动 Rakko 完成更新 must start the new bundle once the helper
	// has swapped it in: that is the path the user's menu item takes.
	pid := 0
	startDeadline := time.Now().Add(30 * time.Second)
	for pid == 0 && time.Now().Before(startDeadline) {
		out, _ := exec.Command("pgrep", "-f", oldApp.exe).Output()
		for _, f := range strings.Fields(string(out)) {
			if n, err := strconv.Atoi(f); err == nil && n != cmd.Process.Pid {
				pid = n
				break
			}
		}
		if pid == 0 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if pid == 0 {
		t.Fatalf("the restarted app did not start; log:\n%s", logBuf.String())
	}
	defer func() { _ = exec.Command("kill", strconv.Itoa(pid)).Run() }()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "Rakko:") {
			t.Logf("app: %s", line)
		}
	}
}
