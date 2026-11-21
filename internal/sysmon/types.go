// Package sysmon samples system performance metrics (CPU, memory, network,
// disk, battery, and top processes) with zero third-party dependencies and
// maintains an in-memory 60-sample rolling history for sparklines and charts.
package sysmon

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// HistoryCap is the number of rolling points kept in memory (60 samples = 1m at 1s).
const HistoryCap = 60

// maxDeltaWindow bounds counter-diff intervals so wake from sleep or clock
// jumps never inject artificial throughput or CPU spikes.
const maxDeltaWindow = 10 * time.Second

// BatteryInfo mirrors the battery state supplied by the main thread.
type BatteryInfo struct {
	Present  bool
	Percent  int
	Charging bool
	External bool
}

// ProcEntry is one process ranked by CPU or memory usage.
type ProcEntry struct {
	PID        int32
	Name       string
	CPUPercent float64
	MemBytes   uint64
}

// RawProc is a single raw process sample prior to CPU-time differentiation.
type RawProc struct {
	PID       int32
	Name      string
	CPUTimeNS uint64
	MemBytes  uint64
}

// Snapshot is one sampled frame ready for cards, wings, and the detail popover.
type Snapshot struct {
	At       time.Time
	// Ready says at least one metric could be read; the per-metric flags
	// say which ones, so a failed counter draws "--" without blanking the
	// rest of the card.
	Ready     bool
	CPUReady  bool
	MemReady  bool
	NetReady  bool
	DiskReady bool

	CPUPercent float64

	MemTotal      uint64
	MemUsed       uint64
	MemApp        uint64
	MemWired      uint64
	MemCompressed uint64
	MemPercent    float64

	NetDownBps uint64
	NetUpBps   uint64

	DiskTotal    uint64
	DiskFree     uint64
	DiskPercent  float64
	DiskReadBps  uint64
	DiskWriteBps uint64

	Battery BatteryInfo

	TopCPU []ProcEntry
	TopMem []ProcEntry
}

// History holds independent copies of the rolling metric ring buffers.
type History struct {
	CPU     []float32
	Mem     []float32
	NetDown []uint64
	NetUp   []uint64
}

// RawReader reads instantaneous OS counters. Implementations live per OS.
type RawReader interface {
	ReadCPU() (busy, total uint64, ok bool)
	ReadMem() (total, used, app, wired, comp uint64, ok bool)
	ReadNet() (rxBytes, txBytes uint64, ok bool)
	// ReadDisk returns filesystem space and throughput. When directRate is true,
	// readVal and writeVal are already bytes/sec (Windows PDH); when false, they
	// are cumulative bytes (macOS IOKit) to be differentiated by Sampler.
	ReadDisk() (total, free, readVal, writeVal uint64, directRate, ok bool)
	ReadProcs() ([]RawProc, bool)
}

// Sampler differentiates raw counters across calls and keeps a 60-sample history.
type Sampler struct {
	reader RawReader

	hasPrev      bool
	prevAt       time.Time
	hasCPU       bool
	prevCPUBusy  uint64
	prevCPUTotal uint64
	hasNet       bool
	prevNetRX    uint64
	prevNetTX    uint64
	hasDiskIO    bool
	prevDiskR    uint64
	prevDiskW    uint64
	prevProcs    map[int32]uint64

	last Snapshot

	cpuHist  []float32
	memHist  []float32
	downHist []uint64
	upHist   []uint64
}

// NewSampler creates a Sampler backed by reader.
func NewSampler(reader RawReader) *Sampler {
	return &Sampler{
		reader:    reader,
		prevProcs: make(map[int32]uint64),
	}
}

// Reset clears differential baselines while preserving nothing stale across sleep.
func (s *Sampler) Reset() {
	s.hasPrev = false
	s.prevAt = time.Time{}
	s.hasCPU = false
	s.hasNet = false
	s.hasDiskIO = false
	clear(s.prevProcs)
}

// Sample captures a new Snapshot at now and appends to the 60-point history.
func (s *Sampler) Sample(now time.Time, wantProcs bool, batt BatteryInfo) Snapshot {
	if s.reader == nil {
		return Snapshot{At: now, Battery: batt}
	}

	var dt time.Duration
	validDelta := false
	if s.hasPrev {
		dt = now.Sub(s.prevAt)
		if dt <= 0 || dt > maxDeltaWindow {
			// Clock jump or wake from sleep: drop differential baselines so the
			// next valid sample starts clean with zero artificial spike.
			s.hasCPU = false
			s.hasNet = false
			s.hasDiskIO = false
			clear(s.prevProcs)
		} else {
			validDelta = true
		}
	}

	snap := Snapshot{
		At:      now,
		Battery: batt,
	}

	// CPU
	if busy, total, ok := s.reader.ReadCPU(); ok {
		if validDelta && s.hasCPU && total > s.prevCPUTotal {
			snap.CPUReady = true
			if busy > s.prevCPUBusy {
				dBusy := busy - s.prevCPUBusy
				dTotal := total - s.prevCPUTotal
				pct := float64(dBusy) * 100 / float64(dTotal)
				snap.CPUPercent = clampFloat(pct, 0, 100)
			}
		}
		s.prevCPUBusy, s.prevCPUTotal = busy, total
		s.hasCPU = true
	}

	// Memory
	memReady := false
	if total, used, app, wired, comp, ok := s.reader.ReadMem(); ok && total > 0 {
		snap.MemTotal = total
		snap.MemUsed = min(used, total)
		snap.MemApp = app
		snap.MemWired = wired
		snap.MemCompressed = comp
		snap.MemPercent = clampFloat(float64(snap.MemUsed)*100/float64(total), 0, 100)
		memReady = true
	}

	// Network
	netReady := false
	if rx, tx, ok := s.reader.ReadNet(); ok {
		if validDelta && s.hasNet {
			secs := dt.Seconds()
			if rx >= s.prevNetRX {
				snap.NetDownBps = uint64(float64(rx-s.prevNetRX) / secs)
			}
			if tx >= s.prevNetTX {
				snap.NetUpBps = uint64(float64(tx-s.prevNetTX) / secs)
			}
			netReady = true
		}
		s.prevNetRX, s.prevNetTX = rx, tx
		s.hasNet = true
	}

	// Disk
	diskReady := false
	if total, free, rVal, wVal, directRate, ok := s.reader.ReadDisk(); ok {
		diskReady = true
		snap.DiskTotal = total
		snap.DiskFree = min(free, total)
		if total > 0 {
			used := total - snap.DiskFree
			snap.DiskPercent = clampFloat(float64(used)*100/float64(total), 0, 100)
		}
		if directRate {
			snap.DiskReadBps = rVal
			snap.DiskWriteBps = wVal
			s.hasDiskIO = false
		} else {
			if validDelta && s.hasDiskIO {
				secs := dt.Seconds()
				if rVal >= s.prevDiskR {
					snap.DiskReadBps = uint64(float64(rVal-s.prevDiskR) / secs)
				}
				if wVal >= s.prevDiskW {
					snap.DiskWriteBps = uint64(float64(wVal-s.prevDiskW) / secs)
				}
			}
			s.prevDiskR, s.prevDiskW = rVal, wVal
			s.hasDiskIO = true
		}
	}

	// Processes (only read when the detail popover needs Top 3). The
	// per-process baseline survives a closed popover, but no list is
	// published without one: a sample with no usable delta would rank every
	// process at 0% by memory, which the popover would show as a CPU
	// ranking. An empty list is the waiting state; the next sample (the
	// module asks for one straight away after opening) ranks by CPU.
	if wantProcs {
		raw, ok := s.reader.ReadProcs()
		if ok {
			var measured bool
			snap.TopCPU, snap.TopMem, measured = s.rankProcs(raw, dt, validDelta)
			if !measured {
				// The memory ranking needs no baseline; only the CPU list
				// waits for a delta (an empty list is the waiting state).
				snap.TopCPU = nil
			}
		} else {
			snap.TopCPU = s.last.TopCPU
			snap.TopMem = s.last.TopMem
		}
	}

	snap.MemReady, snap.NetReady, snap.DiskReady = memReady, netReady, diskReady
	snap.Ready = memReady || netReady || diskReady || snap.CPUReady

	s.prevAt = now
	s.hasPrev = true
	s.last = snap

	if snap.CPUReady {
		s.cpuHist = appendRingFloat32(s.cpuHist, float32(snap.CPUPercent))
	}
	if memReady {
		s.memHist = appendRingFloat32(s.memHist, float32(snap.MemPercent))
	}
	if netReady {
		s.downHist = appendRingUint64(s.downHist, snap.NetDownBps)
		s.upHist = appendRingUint64(s.upHist, snap.NetUpBps)
	}

	return snap
}

func (s *Sampler) rankProcs(raw []RawProc, dt time.Duration, validDelta bool) (topCPU, topMem []ProcEntry, measured bool) {
	nextPrev := make(map[int32]uint64, len(raw))
	entries := make([]ProcEntry, 0, len(raw))
	dtNS := float64(dt.Nanoseconds())
	for _, p := range raw {
		nextPrev[p.PID] = p.CPUTimeNS
		name := cleanProcName(p.Name)
		if name == "" {
			continue
		}
		var cpuPct float64
		if validDelta && dtNS > 0 {
			if prevNS, ok := s.prevProcs[p.PID]; ok && p.CPUTimeNS >= prevNS {
				cpuPct = float64(p.CPUTimeNS-prevNS) * 100 / dtNS
				measured = true
			}
		}
		entries = append(entries, ProcEntry{
			PID:        p.PID,
			Name:       name,
			CPUPercent: max(0, cpuPct),
			MemBytes:   p.MemBytes,
		})
	}
	s.prevProcs = nextPrev

	byCPU := slices.Clone(entries)
	slices.SortStableFunc(byCPU, func(a, b ProcEntry) int {
		if a.CPUPercent > b.CPUPercent {
			return -1
		}
		if a.CPUPercent < b.CPUPercent {
			return 1
		}
		if a.MemBytes > b.MemBytes {
			return -1
		}
		if a.MemBytes < b.MemBytes {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(byCPU) > 3 {
		byCPU = byCPU[:3]
	}

	byMem := entries
	slices.SortStableFunc(byMem, func(a, b ProcEntry) int {
		if a.MemBytes > b.MemBytes {
			return -1
		}
		if a.MemBytes < b.MemBytes {
			return 1
		}
		if a.CPUPercent > b.CPUPercent {
			return -1
		}
		if a.CPUPercent < b.CPUPercent {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(byMem) > 3 {
		byMem = byMem[:3]
	}

	return byCPU, byMem, measured
}

// History returns copied slices of the rolling 60-sample buffers.
func (s *Sampler) History() History {
	return History{
		CPU:     slices.Clone(s.cpuHist),
		Mem:     slices.Clone(s.memHist),
		NetDown: slices.Clone(s.downHist),
		NetUp:   slices.Clone(s.upHist),
	}
}

// Last returns the most recent snapshot.
func (s *Sampler) Last() Snapshot {
	return s.last
}

func appendRingFloat32(buf []float32, v float32) []float32 {
	if len(buf) >= HistoryCap {
		copy(buf, buf[1:])
		buf[len(buf)-1] = v
		return buf
	}
	return append(buf, v)
}

func appendRingUint64(buf []uint64, v uint64) []uint64 {
	if len(buf) >= HistoryCap {
		copy(buf, buf[1:])
		buf[len(buf)-1] = v
		return buf
	}
	return append(buf, v)
}

func cleanProcName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(name), ".exe") && len(name) > 4 {
		name = name[:len(name)-4]
	}
	return filepath.Base(name)
}

func clampFloat(v, lo, hi float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, -1) {
		return lo
	}
	if math.IsInf(v, 1) {
		return hi
	}
	return min(max(v, lo), hi)
}

// FormatPercent formats p rounded and clamped into 0%–100%.
func FormatPercent(p float64) string {
	n := int(math.Round(clampFloat(p, 0, 100)))
	return fmt.Sprintf("%d%%", n)
}

// FormatRate formats bytes/sec concisely without overflow:
// < 1 MiB/s -> K (e.g. 0K, 42K), < 1 GiB/s -> M (18.4M / 112M), >= 1 GiB/s -> G (1.21G).
// When compact is false, appends "/s".
func FormatRate(bps uint64, compact bool) string {
	const (
		kib = 1024
		mib = 1024 * 1024
		gib = 1024 * 1024 * 1024
	)
	var out string
	switch {
	case bps >= gib:
		g := float64(bps) / float64(gib)
		if g >= 10 {
			out = fmt.Sprintf("%.1fG", g)
		} else {
			out = fmt.Sprintf("%.2fG", g)
		}
	case bps >= mib:
		m := float64(bps) / float64(mib)
		if m >= 100 {
			out = fmt.Sprintf("%.0fM", m)
		} else {
			out = fmt.Sprintf("%.1fM", m)
		}
	default:
		k := math.Round(float64(bps) / float64(kib))
		out = fmt.Sprintf("%.0fK", k)
	}
	if !compact {
		out += "/s"
	}
	return out
}

// FormatBytesShort formats byte capacities into compact T / G / M labels.
func FormatBytesShort(b uint64) string {
	const (
		mib = 1024 * 1024
		gib = 1024 * 1024 * 1024
		tib = 1024 * 1024 * 1024 * 1024
	)
	switch {
	case b >= tib:
		t := float64(b) / float64(tib)
		if t >= 10 {
			return fmt.Sprintf("%.1fT", t)
		}
		return fmt.Sprintf("%.2fT", t)
	case b >= gib:
		g := float64(b) / float64(gib)
		if g >= 100 {
			return fmt.Sprintf("%.0fG", g)
		}
		s := fmt.Sprintf("%.1f", g)
		s = strings.TrimSuffix(s, ".0")
		return s + "G"
	default:
		m := float64(b) / float64(mib)
		return fmt.Sprintf("%.0fM", math.Round(m))
	}
}

// FormatMemPair formats memory used/total as e.g. "25.9/32G".
func FormatMemPair(used, total uint64) string {
	const gib = 1024 * 1024 * 1024
	if total == 0 {
		return "0/0G"
	}
	u := float64(used) / float64(gib)
	t := float64(total) / float64(gib)
	uStr := fmt.Sprintf("%.1f", u)
	var tStr string
	if math.Abs(t-math.Round(t)) < 0.05 {
		tStr = fmt.Sprintf("%.0f", math.Round(t))
	} else {
		tStr = fmt.Sprintf("%.1f", t)
	}
	return uStr + "/" + tStr + "G"
}
