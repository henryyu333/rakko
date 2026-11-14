//go:build windows

package platform

import (
	"reflect"
	"testing"
)

// TestConsumeServiceArgs checks the Explorer launch line: the verb is
// removed from the app's own arguments, the quoted paths reach the
// receiver once, and a plain launch is untouched.
func TestConsumeServiceArgs(t *testing.T) {
	var got []string
	defer func() {
		serviceLaunch.mu.Lock()
		serviceLaunch.fn, serviceLaunch.pending = nil, nil
		serviceLaunch.mu.Unlock()
	}()
	if err := InstallAddToRakko(func(paths []string, text string) {
		if text != "" {
			t.Errorf("text = %q, want empty", text)
		}
		got = append(got, paths...)
	}); err != nil {
		t.Fatal(err)
	}

	rest := ConsumeServiceArgs([]string{"--add-to-rakko", `C:\a b\one.txt`, `D:\two.txt`})
	if len(rest) != 0 {
		t.Errorf("rest = %q, want none", rest)
	}
	want := []string{`C:\a b\one.txt`, `D:\two.txt`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload = %q, want %q", got, want)
	}

	plain := []string{"--data-dir", `C:\data`}
	if got := ConsumeServiceArgs(plain); !reflect.DeepEqual(got, plain) {
		t.Errorf("plain launch changed: %q", got)
	}
}

// TestConsumeServiceArgsBeforeReady checks a cold start's payload is held
// until the receiver is installed.
func TestConsumeServiceArgsBeforeReady(t *testing.T) {
	serviceLaunch.mu.Lock()
	serviceLaunch.fn, serviceLaunch.pending = nil, nil
	serviceLaunch.mu.Unlock()
	defer func() {
		serviceLaunch.mu.Lock()
		serviceLaunch.fn, serviceLaunch.pending = nil, nil
		serviceLaunch.mu.Unlock()
	}()
	ConsumeServiceArgs([]string{"--add-to-rakko", `C:\held.txt`})
	var got []string
	if err := InstallAddToRakko(func(paths []string, text string) { got = append(got, paths...) }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{`C:\held.txt`}) {
		t.Errorf("drained payload = %q", got)
	}
}
