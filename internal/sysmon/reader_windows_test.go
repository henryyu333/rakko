//go:build windows

package sysmon

import (
	"testing"
	"time"
	"unsafe"
)

func TestWindowsStructLayouts(t *testing.T) {
	if got := unsafe.Sizeof(mibIfRow2{}); got != 1352 {
		t.Fatalf("sizeof(mibIfRow2) = %d, want 1352", got)
	}
	if got := unsafe.Offsetof(mibIfRow2{}.InOctets); got != 1208 {
		t.Fatalf("offsetof(InOctets) = %d, want 1208", got)
	}
	if got := unsafe.Offsetof(mibIfRow2{}.OutOctets); got != 1280 {
		t.Fatalf("offsetof(OutOctets) = %d, want 1280", got)
	}
	if got := unsafe.Sizeof(performanceInformation{}); got != 104 {
		t.Fatalf("sizeof(performanceInformation) = %d, want 104", got)
	}
	if got := unsafe.Sizeof(memoryStatusEx{}); got != 64 {
		t.Fatalf("sizeof(memoryStatusEx) = %d, want 64", got)
	}
	if got := unsafe.Sizeof(procMemCounters{}); got != 72 {
		t.Fatalf("sizeof(procMemCounters) = %d, want 72", got)
	}
}

func TestWindowsPlatformReaderSample(t *testing.T) {
	s := NewSampler(NewPlatformReader())
	now := time.Now()
	s1 := s.Sample(now, true, BatteryInfo{})
	if !s1.Ready {
		t.Fatal("first windows sample not ready")
	}
	if s1.MemTotal == 0 || s1.DiskTotal == 0 {
		t.Fatalf("invalid gauges: memTotal=%d diskTotal=%d", s1.MemTotal, s1.DiskTotal)
	}
	time.Sleep(120 * time.Millisecond)
	s2 := s.Sample(time.Now(), true, BatteryInfo{})
	if !s2.CPUReady {
		t.Fatal("second windows sample CPUReady=false")
	}
	if len(s2.TopMem) == 0 {
		t.Fatal("expected non-empty TopMem on windows")
	}
}
