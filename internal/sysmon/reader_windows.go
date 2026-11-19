//go:build windows

package sysmon

import (
	"os"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modIPHlpAPI = windows.NewLazySystemDLL("iphlpapi.dll")
	modPDH      = windows.NewLazySystemDLL("pdh.dll")

	procGetSystemTimes          = modKernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx    = modKernel32.NewProc("GlobalMemoryStatusEx")
	procK32GetPerformanceInfo   = modKernel32.NewProc("K32GetPerformanceInfo")
	procK32GetProcessMemoryInfo = modKernel32.NewProc("K32GetProcessMemoryInfo")

	procGetIfTable2  = modIPHlpAPI.NewProc("GetIfTable2")
	procFreeMibTable = modIPHlpAPI.NewProc("FreeMibTable")

	procPdhOpenQueryW               = modPDH.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW       = modPDH.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData         = modPDH.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterValue = modPDH.NewProc("PdhGetFormattedCounterValue")
	procPdhCloseQuery               = modPDH.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble      = 0x00000200
	pdhCStatusValid   = 0x00000000
	pdhCStatusNewData = 0x00000001
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

type performanceInformation struct {
	CB                uint32
	_                 uint32
	CommitTotal       uintptr
	CommitLimit       uintptr
	CommitPeak        uintptr
	PhysicalTotal     uintptr
	PhysicalAvailable uintptr
	SystemCache       uintptr
	KernelTotal       uintptr
	KernelPaged       uintptr
	KernelNonpaged    uintptr
	PageSize          uintptr
	HandleCount       uint32
	ProcessCount      uint32
	ThreadCount       uint32
	_                 uint32
}

type procMemCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// mibIfRow2 matches Windows SDK MIB_IF_ROW2 on 64-bit Windows (total 1352 bytes,
// InOctets @ 1208, OutOctets @ 1280).
type mibIfRow2 struct {
	InterfaceLuid               uint64
	InterfaceIndex              uint32
	InterfaceGuid               windows.GUID
	Alias                       [257]uint16
	Description                 [257]uint16
	PhysicalAddressLength       uint32
	PhysicalAddress             [32]byte
	PermanentPhysicalAddress    [32]byte
	Mtu                         uint32
	Type                        uint32
	TunnelType                  uint32
	MediaType                   uint32
	PhysicalMediumType          uint32
	AccessType                  uint32
	DirectionType               uint32
	InterfaceAndOperStatusFlags byte
	_                           [3]byte
	OperStatus                  uint32
	AdminStatus                 uint32
	MediaConnectState           uint32
	NetworkGuid                 windows.GUID
	ConnectionType              uint32
	_                           uint32
	TransmitLinkSpeed           uint64
	ReceiveLinkSpeed            uint64
	InOctets                    uint64
	InUcastPkts                 uint64
	InNUcastPkts                uint64
	InDiscards                  uint64
	InErrors                    uint64
	InUnknownProtos             uint64
	InUcastOctets               uint64
	InMulticastOctets           uint64
	InBroadcastOctets           uint64
	OutOctets                   uint64
	OutUcastPkts                uint64
	OutNUcastPkts               uint64
	OutDiscards                 uint64
	OutErrors                   uint64
	OutUcastOctets              uint64
	OutMulticastOctets          uint64
	OutBroadcastOctets          uint64
	OutQLen                     uint64
}

type pdhFmtCounterValue struct {
	CStatus     uint32
	_           uint32
	DoubleValue float64
}

type windowsReader struct {
	pdhMu       sync.Mutex
	pdhInited   bool
	pdhOK       bool
	pdhQuery    uintptr
	pdhReadCtr  uintptr
	pdhWriteCtr uintptr
}

// NewPlatformReader returns the Windows RawReader backed by Win32, IP Helper, and PDH.
func NewPlatformReader() RawReader {
	return &windowsReader{}
}

func ftToU64(ft windows.Filetime) uint64 {
	return (uint64(ft.HighDateTime) << 32) | uint64(ft.LowDateTime)
}

func (*windowsReader) ReadCPU() (busy, total uint64, ok bool) {
	var idle, kernel, user windows.Filetime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return 0, 0, false
	}
	idleU := ftToU64(idle)
	kernelU := ftToU64(kernel)
	userU := ftToU64(user)
	// kernel time on Windows includes idle time.
	var kernelBusy uint64
	if kernelU >= idleU {
		kernelBusy = kernelU - idleU
	}
	busy = kernelBusy + userU
	total = kernelU + userU
	return busy, total, total > 0
}

func (*windowsReader) ReadMem() (total, used, app, wired, comp uint64, ok bool) {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r == 0 || ms.TotalPhys == 0 {
		return 0, 0, 0, 0, 0, false
	}
	total = ms.TotalPhys
	if ms.AvailPhys <= total {
		used = total - ms.AvailPhys
	}

	var pi performanceInformation
	pi.CB = uint32(unsafe.Sizeof(pi))
	if pr, _, _ := procK32GetPerformanceInfo.Call(uintptr(unsafe.Pointer(&pi)), uintptr(pi.CB)); pr != 0 && pi.PageSize > 0 {
		page := uint64(pi.PageSize)
		wired = (uint64(pi.KernelPaged) + uint64(pi.KernelNonpaged)) * page
		if wired > used {
			wired = used
		}
		comp = uint64(pi.SystemCache) * page
		if wired+comp > used {
			comp = used - wired
		}
		app = used - wired - comp
	} else {
		app = used
	}
	return total, used, app, wired, comp, true
}

func (*windowsReader) ReadNet() (rxBytes, txBytes uint64, ok bool) {
	var tablePtr uintptr
	r, _, _ := procGetIfTable2.Call(uintptr(unsafe.Pointer(&tablePtr)))
	if r != 0 || tablePtr == 0 {
		return 0, 0, false
	}
	defer procFreeMibTable.Call(tablePtr)

	numEntries := *(*uint32)(unsafe.Pointer(tablePtr))
	rowBase := tablePtr + 8
	rowSize := unsafe.Sizeof(mibIfRow2{})
	for i := uint32(0); i < numEntries; i++ {
		row := (*mibIfRow2)(unsafe.Pointer(rowBase + uintptr(i)*rowSize))
		if row.OperStatus != 1 { // IfOperStatusUp
			continue
		}
		// Ethernet (6), IEEE 802.11 Wi-Fi (71), WWAN PP (243), WWAN PP2 (244).
		switch row.Type {
		case 6, 71, 243, 244:
		default:
			continue
		}
		// Bit 0: HardwareInterface (0x01), Bit 1: FilterInterface (0x02).
		if row.InterfaceAndOperStatusFlags&0x01 == 0 || row.InterfaceAndOperStatusFlags&0x02 != 0 {
			continue
		}
		rxBytes += row.InOctets
		txBytes += row.OutOctets
	}
	return rxBytes, txBytes, true
}

func (w *windowsReader) ReadDisk() (total, free, readBps, writeBps uint64, directRate, ok bool) {
	drive := strings.TrimSpace(os.Getenv("SystemDrive"))
	if drive == "" {
		drive = "C:"
	}
	if !strings.HasSuffix(drive, `\`) {
		drive += `\`
	}
	drivePtr, err := windows.UTF16PtrFromString(drive)
	if err != nil {
		return 0, 0, 0, 0, true, false
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(drivePtr, &avail, &total, nil); err != nil || total == 0 {
		return 0, 0, 0, 0, true, false
	}
	free = min(avail, total)

	readBps, writeBps = w.readPDHDiskRates()
	return total, free, readBps, writeBps, true, true
}

func (w *windowsReader) readPDHDiskRates() (readBps, writeBps uint64) {
	w.pdhMu.Lock()
	defer w.pdhMu.Unlock()

	if !w.pdhInited {
		w.pdhInited = true
		var q uintptr
		if r, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&q))); r == 0 && q != 0 {
			rPath, _ := windows.UTF16PtrFromString(`\PhysicalDisk(_Total)\Disk Read Bytes/sec`)
			wPath, _ := windows.UTF16PtrFromString(`\PhysicalDisk(_Total)\Disk Write Bytes/sec`)
			var rc, wc uintptr
			r1, _, _ := procPdhAddEnglishCounterW.Call(q, uintptr(unsafe.Pointer(rPath)), 0, uintptr(unsafe.Pointer(&rc)))
			r2, _, _ := procPdhAddEnglishCounterW.Call(q, uintptr(unsafe.Pointer(wPath)), 0, uintptr(unsafe.Pointer(&wc)))
			if r1 == 0 && r2 == 0 {
				_, _, _ = procPdhCollectQueryData.Call(q)
				w.pdhQuery = q
				w.pdhReadCtr = rc
				w.pdhWriteCtr = wc
				w.pdhOK = true
				return 0, 0
			} else {
				_, _, _ = procPdhCloseQuery.Call(q)
			}
		}
	}
	if !w.pdhOK || w.pdhQuery == 0 {
		return 0, 0
	}
	if r, _, _ := procPdhCollectQueryData.Call(w.pdhQuery); r != 0 {
		return 0, 0
	}
	var rv, wv pdhFmtCounterValue
	if r, _, _ := procPdhGetFormattedCounterValue.Call(w.pdhReadCtr, pdhFmtDouble, 0, uintptr(unsafe.Pointer(&rv))); r == 0 && (rv.CStatus == pdhCStatusValid || rv.CStatus == pdhCStatusNewData) && rv.DoubleValue > 0 {
		readBps = uint64(rv.DoubleValue)
	}
	if r, _, _ := procPdhGetFormattedCounterValue.Call(w.pdhWriteCtr, pdhFmtDouble, 0, uintptr(unsafe.Pointer(&wv))); r == 0 && (wv.CStatus == pdhCStatusValid || wv.CStatus == pdhCStatusNewData) && wv.DoubleValue > 0 {
		writeBps = uint64(wv.DoubleValue)
	}
	return readBps, writeBps
}

func (*windowsReader) ReadProcs() ([]RawProc, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, false
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, false
	}

	var out []RawProc
	for {
		pid := int32(pe.ProcessID)
		if pid > 0 {
			name := windows.UTF16ToString(pe.ExeFile[:])
			if name != "" {
				h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID)
				if err == nil {
					var ctime, etime, ktime, utime windows.Filetime
					var cpuNS uint64
					if windows.GetProcessTimes(h, &ctime, &etime, &ktime, &utime) == nil {
						cpuNS = (ftToU64(ktime) + ftToU64(utime)) * 100
					}
					var pmc procMemCounters
					pmc.CB = uint32(unsafe.Sizeof(pmc))
					var memBytes uint64
					if r, _, _ := procK32GetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.CB)); r != 0 {
						memBytes = uint64(pmc.WorkingSetSize)
					}
					_ = windows.CloseHandle(h)
					out = append(out, RawProc{
						PID:       pid,
						Name:      name,
						CPUTimeNS: cpuNS,
						MemBytes:  memBytes,
					})
				}
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return out, len(out) > 0
}
