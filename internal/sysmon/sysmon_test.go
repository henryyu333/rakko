package sysmon

import (
	"testing"
	"time"
)

type fakeReader struct {
	cpuBusy, cpuTotal           uint64
	memTotal, memUsed           uint64
	memApp, memWired, memComp   uint64
	netRX, netTX                uint64
	diskTotal, diskFree         uint64
	diskR, diskW                uint64
	diskDirect                  bool
	procs                       []RawProc
	procCalls                   int
}

func (f *fakeReader) ReadCPU() (uint64, uint64, bool) { return f.cpuBusy, f.cpuTotal, true }
func (f *fakeReader) ReadMem() (uint64, uint64, uint64, uint64, uint64, bool) {
	return f.memTotal, f.memUsed, f.memApp, f.memWired, f.memComp, true
}
func (f *fakeReader) ReadNet() (uint64, uint64, bool) { return f.netRX, f.netTX, true }
func (f *fakeReader) ReadDisk() (uint64, uint64, uint64, uint64, bool, bool) {
	return f.diskTotal, f.diskFree, f.diskR, f.diskW, f.diskDirect, true
}
func (f *fakeReader) ReadProcs() ([]RawProc, bool) {
	f.procCalls++
	return f.procs, true
}

func TestSamplerDiffSleepWrapAndRing(t *testing.T) {
	const gib = uint64(1024 * 1024 * 1024)
	r := &fakeReader{
		cpuBusy: 1000, cpuTotal: 2000,
		memTotal: 32 * gib, memUsed: 16 * gib, memApp: 10 * gib, memWired: 4 * gib, memComp: 2 * gib,
		netRX: 100000, netTX: 20000,
		diskTotal: 500 * gib, diskFree: 200 * gib,
		diskR: 5000, diskW: 2000,
		procs: []RawProc{
			{PID: 10, Name: "clang++.exe", CPUTimeNS: 100_000_000, MemBytes: 4 * gib},
			{PID: 11, Name: "go", CPUTimeNS: 50_000_000, MemBytes: 2 * gib},
			{PID: 12, Name: "WindowServer", CPUTimeNS: 10_000_000, MemBytes: 6 * gib},
			{PID: 13, Name: "Finder", CPUTimeNS: 5_000_000, MemBytes: 1 * gib},
		},
	}
	s := NewSampler(r)
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

	// First sample: instant gauges ready, differential CPU/rates still 0 and absent from history.
	s1 := s.Sample(t0, false, BatteryInfo{Present: true, Percent: 80})
	if !s1.Ready || s1.CPUReady {
		t.Fatalf("first sample Ready=%v CPUReady=%v", s1.Ready, s1.CPUReady)
	}
	if r.procCalls != 0 {
		t.Fatalf("wantProcs=false called ReadProcs %d times", r.procCalls)
	}
	if s1.MemPercent != 50 || s1.DiskPercent != 60 {
		t.Fatalf("mem=%.1f disk=%.1f", s1.MemPercent, s1.DiskPercent)
	}
	h1 := s.History()
	if len(h1.CPU) != 0 || len(h1.NetDown) != 0 || len(h1.NetUp) != 0 || len(h1.Mem) != 1 {
		t.Fatalf("first sample history len cpu=%d down=%d up=%d mem=%d", len(h1.CPU), len(h1.NetDown), len(h1.NetUp), len(h1.Mem))
	}

	// Second sample after 1s: CPU 50%, net 10MB/s down, 1MB/s up, disk R/W.
	r.cpuBusy += 500
	r.cpuTotal += 1000
	r.netRX += 10 * 1024 * 1024
	r.netTX += 1 * 1024 * 1024
	r.diskR += 2 * 1024 * 1024
	r.diskW += 512 * 1024
	s2 := s.Sample(t0.Add(time.Second), true, BatteryInfo{Present: true, Percent: 81, Charging: true})
	if !s2.CPUReady || s2.CPUPercent != 50 {
		t.Fatalf("CPUReady=%v CPUPercent=%.2f, want 50", s2.CPUReady, s2.CPUPercent)
	}
	if s2.NetDownBps != 10*1024*1024 || s2.NetUpBps != 1024*1024 {
		t.Fatalf("net down=%d up=%d", s2.NetDownBps, s2.NetUpBps)
	}
	if s2.DiskReadBps != 2*1024*1024 || s2.DiskWriteBps != 512*1024 {
		t.Fatalf("disk R=%d W=%d", s2.DiskReadBps, s2.DiskWriteBps)
	}
	if len(s2.TopMem) != 3 || s2.TopMem[0].Name != "WindowServer" || s2.TopMem[1].Name != "clang++" {
		t.Fatalf("TopMem = %+v", s2.TopMem)
	}

	// Third sample after 1s: check TopCPU delta and .exe trimming.
	r.cpuBusy += 800
	r.cpuTotal += 1000
	r.procs[0].CPUTimeNS += 400_000_000 // 40%
	r.procs[1].CPUTimeNS += 250_000_000 // 25%
	r.procs[2].CPUTimeNS += 100_000_000 // 10%
	r.procs[3].CPUTimeNS += 20_000_000  // 2%
	s3 := s.Sample(t0.Add(2*time.Second), true, BatteryInfo{})
	if len(s3.TopCPU) != 3 || s3.TopCPU[0].Name != "clang++" || s3.TopCPU[0].CPUPercent < 39.9 || s3.TopCPU[0].CPUPercent > 40.1 {
		t.Fatalf("TopCPU = %+v", s3.TopCPU)
	}

	// Sleep jump (>10s) must reset differential baselines without emitting spikes.
	r.cpuBusy += 9000
	r.cpuTotal += 10000
	r.netRX += 500 * 1024 * 1024
	sSleep := s.Sample(t0.Add(30*time.Second), true, BatteryInfo{})
	if sSleep.CPUReady || sSleep.NetDownBps != 0 || sSleep.NetUpBps != 0 || sSleep.DiskReadBps != 0 {
		t.Fatalf("sleep jump leaked rate/cpu: %+v", sSleep)
	}

	// Counter wrap / drop protection: smaller values must yield 0, not uint64 underflow.
	r.netRX -= 100
	r.cpuBusy -= 10
	sWrap := s.Sample(t0.Add(31*time.Second), false, BatteryInfo{})
	if sWrap.NetDownBps != 0 || sWrap.CPUPercent != 0 {
		t.Fatalf("counter drop underflowed: %+v", sWrap)
	}

	// Direct disk rate mode (Windows PDH).
	r.diskDirect = true
	r.diskR = 186 * 1024 * 1024
	r.diskW = 94 * 1024 * 1024
	sDirect := s.Sample(t0.Add(32*time.Second), false, BatteryInfo{})
	if sDirect.DiskReadBps != 186*1024*1024 || sDirect.DiskWriteBps != 94*1024*1024 {
		t.Fatalf("direct disk rate = %d / %d", sDirect.DiskReadBps, sDirect.DiskWriteBps)
	}

	// History cap at HistoryCap (60).
	curT := t0.Add(33 * time.Second)
	for i := 0; i < 70; i++ {
		r.cpuBusy += 100
		r.cpuTotal += 1000
		s.Sample(curT, false, BatteryInfo{})
		curT = curT.Add(time.Second)
	}
	h := s.History()
	if len(h.CPU) != HistoryCap || len(h.Mem) != HistoryCap || len(h.NetDown) != HistoryCap || len(h.NetUp) != HistoryCap {
		t.Fatalf("history lengths = %d/%d/%d/%d, want %d", len(h.CPU), len(h.Mem), len(h.NetDown), len(h.NetUp), HistoryCap)
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := FormatPercent(-3); got != "0%" {
		t.Fatalf("FormatPercent(-3) = %q", got)
	}
	if got := FormatPercent(104.2); got != "100%" {
		t.Fatalf("FormatPercent(104.2) = %q", got)
	}
	if got := FormatRate(42*1024, true); got != "42K" {
		t.Fatalf("FormatRate 42K = %q", got)
	}
	if got := FormatRate(184*1024*1024/10, false); got != "18.4M/s" {
		t.Fatalf("FormatRate 18.4M = %q", got)
	}
	if got := FormatRate(112*1024*1024, true); got != "112M" {
		t.Fatalf("FormatRate 112M = %q", got)
	}
	if got := FormatRate(121*1024*1024*1024/100, true); got != "1.21G" {
		t.Fatalf("FormatRate 1.21G = %q", got)
	}
	if got := FormatBytesShort(104 * 1024 * 1024 * 1024 * 1024 / 100); got != "1.04T" {
		t.Fatalf("FormatBytesShort 1.04T = %q", got)
	}
	if got := FormatMemPair(259*1024*1024*1024/10, 32*1024*1024*1024); got != "25.9/32G" {
		t.Fatalf("FormatMemPair = %q", got)
	}
}
