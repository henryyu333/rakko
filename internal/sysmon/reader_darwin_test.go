//go:build darwin

package sysmon

import (
	"testing"
	"time"
)

func TestDarwinPlatformReaderSample(t *testing.T) {
	s := NewSampler(NewPlatformReader())
	now := time.Now()
	s1 := s.Sample(now, true, BatteryInfo{})
	if !s1.Ready {
		t.Fatal("first darwin sample not ready")
	}
	if s1.MemTotal == 0 || s1.MemUsed == 0 {
		t.Fatalf("invalid memory: total=%d used=%d", s1.MemTotal, s1.MemUsed)
	}
	if s1.DiskTotal == 0 {
		t.Fatalf("invalid disk total=%d", s1.DiskTotal)
	}
	if len(s1.TopMem) == 0 {
		t.Fatal("expected non-empty TopMem on darwin")
	}

	time.Sleep(120 * time.Millisecond)
	s2 := s.Sample(time.Now(), true, BatteryInfo{})
	if !s2.CPUReady {
		t.Fatal("second darwin sample CPUReady=false")
	}
	if s2.CPUPercent < 0 || s2.CPUPercent > 100 {
		t.Fatalf("unexpected CPU percent: %.2f", s2.CPUPercent)
	}
	if len(s2.TopCPU) == 0 {
		t.Fatal("expected non-empty TopCPU on darwin")
	}
}
