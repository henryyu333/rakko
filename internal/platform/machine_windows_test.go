//go:build windows

package platform

// Some platform checks need what a desktop Windows has and a hosted CI
// runner does not: a sound device, a Chinese OCR language, a running search
// index. These helpers ask the machine, so a test skips only where the
// capability is missing and still fails on a machine that has it.

import (
	"os/exec"
	"strings"
	"testing"
)

// powershellOut runs one PowerShell expression and returns its trimmed
// output, or "" when PowerShell itself fails.
func powershellOut(t *testing.T, expr string) string {
	t.Helper()
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", expr).Output()
	if err != nil {
		t.Logf("powershell %q: %v", expr, err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

func requireSoundDevice(t *testing.T) {
	t.Helper()
	if n := powershellOut(t, `@(Get-CimInstance Win32_SoundDevice -ErrorAction SilentlyContinue).Count`); n == "" || n == "0" {
		t.Skip("this machine has no sound device")
	}
}

func requireChineseOCR(t *testing.T) {
	t.Helper()
	langs := powershellOut(t, `[void][Windows.Media.Ocr.OcrEngine,Windows.Foundation,ContentType=WindowsRuntime]; ([Windows.Media.Ocr.OcrEngine]::AvailableRecognizerLanguages | ForEach-Object { $_.LanguageTag }) -join ','`)
	if !strings.Contains(langs, "zh-") {
		t.Skipf("no Chinese OCR language on this machine (installed: %q)", langs)
	}
}

func requireSearchIndex(t *testing.T) {
	t.Helper()
	if st := powershellOut(t, `(Get-Service WSearch -ErrorAction SilentlyContinue).Status`); st != "Running" {
		t.Skipf("the Windows Search service is not running (status %q)", st)
	}
}
