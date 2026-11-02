//go:build darwin

package main

import (
	"archive/zip"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"

	"rakko/internal/mac"
)

// Real-window drop-zone checks, run only under RAKKO_BASKET_SMOKE=1 so the
// shared TestMain already hosts the app loop. Shots land in
// /tmp/shelf-fix/drop-zone/<name>.png; the instance works in the smoke data
// dir and never touches ~/Library/Application Support/ShelfU.

const dropShotDir = "/tmp/shelf-fix/drop-zone"

// dropSmokeShelf is the live shelf with its converter, the way ready()
// wires it, minus the observers a screenshot pass does not need.
func dropSmokeShelf(t *testing.T) *shelf {
	t.Helper()
	a := smokeShelf
	if a == nil {
		t.Fatal("smoke shelf missing")
	}
	if a.conv == nil {
		a.conv = &converter{host: a}
		a.modules = append(a.modules, a.conv)
	}
	if a.win == nil {
		onMain(func() { a.newNotchWindow() })
	}
	if a.win == nil {
		t.Fatal("no notch window")
	}
	onMain(func() {
		// Each smoke test gets an empty slate: the drop-zone snapshot
		// holds the file set of the last drag that drew cells (shotDropZone
		// feeds it through RAKKO_DEBUG_DROP_FILES), and runDropAction
		// prefers it over the data actually dropped — a leftover set would
		// convert the previous test's deleted tempdir instead of this
		// test's files. Converted outputs likewise stay in the shared tray
		// and would confuse findConverted.
		a.drop = dropZoneState{}
		for _, it := range a.store.Items {
			if it.Pinned {
				_ = a.store.SetPinned(it.ID, false)
			}
		}
		_ = a.store.Clear()
		a.selection.clear()
	})
	return a
}

// writeNoisyPNG encodes a pseudo-random PNG that compresses badly, so it
// stays over the 1 MB budget for 压缩到.
func writeNoisyPNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(1234567)
	for y := range h {
		for x := range w {
			// xorshift32: deterministic noise, no imports.
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			img.Set(x, y, color.RGBA{uint8(seed), uint8(seed >> 8), uint8(seed >> 16), 255})
		}
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeSmokePNG encodes a gradient PNG — big enough to exercise 压缩到.
func writeSmokePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + y) % 256), 255})
		}
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

// shotDropZone pins the zone's contents with RAKKO_DEBUG_DROP_FILES and
// screenshots the real panel window into dropShotDir.
func shotDropZone(t *testing.T, a *shelf, paths []string, name string) {
	t.Helper()
	t.Setenv("RAKKO_DEBUG_DROP_FILES", strings.Join(paths, string(filepath.ListSeparator)))
	onMain(func() {
		if !a.visible {
			a.show(false)
		}
		a.win.Invalidate()
	})
	time.Sleep(900 * time.Millisecond) // settle the window and thumbnails
	onMain(a.syncWindowFrame)
	if err := os.MkdirAll(dropShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	shot := filepath.Join(dropShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
}

func TestDropZoneScreenshots(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := dropSmokeShelf(t)
	fix := t.TempDir()
	defer t.Setenv("RAKKO_DEBUG_DROP_FILES", "")

	// Three dragged-in shapes from the design: three pictures, one
	// archive, a folder with a file.
	imgs := []string{
		writeSmokePNG(t, fix, "照片 一.png", 320, 240),
		writeSmokePNG(t, fix, "照片 二.png", 320, 240),
		writeSmokePNG(t, fix, "照片 三.png", 320, 240),
	}
	shotDropZone(t, a, imgs, "zone-images")
	shotDropZone(t, a, imgs[:1], "zone-single-image")

	zipPath := filepath.Join(fix, "素材.zip")
	payload := filepath.Join(fix, "inner.txt")
	if err := os.WriteFile(payload, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeZip(zipPath, []string{payload}, []string{"inner.txt"}, &convertJob{}); err != nil {
		t.Fatal(err)
	}
	shotDropZone(t, a, []string{zipPath}, "zone-archive")

	folder := filepath.Join(fix, "文件夹")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "inner.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	shotDropZone(t, a, []string{folder, payload}, "zone-mixed")

	onMain(func() { a.hide(hideExplicit) })
}

// TestDropActionsRun drives the three execution paths against real files:
// 压缩到 1 MB, 解压 and 打成 zip each end with their output frontmost in
// the tray and the source files unshelved.
func TestDropActionsRun(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := dropSmokeShelf(t)
	dir := t.TempDir()

	awaitJob := func(what string) {
		t.Helper()
		for range 600 {
			done := true
			onMain(func() { done = !a.conv.busy() })
			if done {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s: conversion never finished", what)
	}
	drop := func(paths []string, actionID string) {
		t.Helper()
		var act *dropAction
		for _, c := range dropActions(paths) {
			if c.id == actionID {
				cc := c
				act = &cc
				break
			}
		}
		if act == nil {
			t.Fatalf("no action %q for %v", actionID, paths)
		}
		data, err := transfer.FileData(paths...)
		if err != nil {
			t.Fatal(err)
		}
		onMain(func() { a.runDropAction(*act, data) })
	}

	// 压缩到 1 MB on a file over the budget.
	big := writeNoisyPNG(t, dir, "大图.png", 1600, 1200)
	drop([]string{big}, "fit1m")
	awaitJob("压缩到 1 MB")
	fit := findConverted(t, a, ".jpg")
	if fi, err := os.Stat(fit); err != nil || fi.Size() >= 1_000_000 {
		t.Fatalf("compressed output %v %v, want < 1 MB", fi, err)
	}
	if fi, _ := os.Stat(fit); fi != nil {
		t.Logf("压缩到: %s %.0f bytes", filepath.Base(fit), float64(fi.Size()))
	}

	// 解压 a real archive.
	zipSrc := filepath.Join(dir, "素材.zip")
	payload := filepath.Join(dir, "内文.txt")
	if err := os.WriteFile(payload, []byte("内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeZip(zipSrc, []string{payload}, []string{"内文.txt"}, &convertJob{}); err != nil {
		t.Fatal(err)
	}
	drop([]string{zipSrc}, "extract")
	awaitJob("解压")
	extracted := findConverted(t, a, "")
	if got, err := os.ReadFile(filepath.Join(extracted, "内文.txt")); err != nil || string(got) != "内容" {
		t.Fatalf("extracted file: %q %v", got, err)
	}

	// 打成 zip on a folder + file mix.
	folder := filepath.Join(dir, "文件堆")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "n.txt"), []byte("n"), 0o600); err != nil {
		t.Fatal(err)
	}
	drop([]string{folder, payload}, "zip")
	awaitJob("打成 zip")
	zr, err := zip.OpenReader(findConverted(t, a, ".zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, e := range zr.File {
		names = append(names, e.Name)
	}
	if !slices.Contains(names, "文件堆/n.txt") || !slices.Contains(names, "内文.txt") {
		t.Fatalf("zip entries = %v", names)
	}

	// The dropped sources themselves were never shelved.
	for _, it := range a.store.Items {
		for _, src := range []string{big, zipSrc, folder, payload} {
			if it.Path == src {
				t.Fatalf("dropped source %s was shelved", src)
			}
		}
	}
}

// findConverted returns the newest converted item with the extension.
func findConverted(t *testing.T, a *shelf, ext string) string {
	t.Helper()
	for _, it := range a.store.Items {
		if it.Converted && (ext == "" || strings.EqualFold(filepath.Ext(it.Path), ext)) {
			return it.Path
		}
	}
	t.Fatalf("no converted *%s item; tray = %v", ext, a.store.Items)
	return ""
}
