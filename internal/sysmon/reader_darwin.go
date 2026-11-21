//go:build darwin

package sysmon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

const (
	hostCPULoadInfo      = 3
	hostCPULoadInfoCount = 4
	hostVMInfo64         = 4
	hostVMInfo64Count    = 104
	procPIDTaskInfo      = 4
	procPIDTaskInfoSize  = 96

	ctlNet       = 4
	pfRoute      = 17
	netRTIfList2 = 6
	rtmIfInfo2   = 18
	iffUp        = 0x1
	iffLoopback  = 0x8

	cfStringEncodingUTF8 = 0x08000100
	cfNumberSInt64Type   = 4
)

type darwinReader struct{}

// NewPlatformReader returns the macOS RawReader backed by Mach, sysctl, libproc, and IOKit.
func NewPlatformReader() RawReader {
	return darwinReader{}
}

var (
	darwinOnce sync.Once
	darwinErr  error

	darwinHostPort uint32
	darwinPageSize uint64
	darwinMemTotal uint64
	darwinTB       struct{ numer, denom uint32 }

	fnMachHostSelf     func() uint32
	fnHostStatistics   func(host uint32, flavor int32, info unsafe.Pointer, count *uint32) int32
	fnHostStatistics64 func(host uint32, flavor int32, info unsafe.Pointer, count *uint32) int32
	fnSysctl           func(mib *int32, namelen uint32, oldp unsafe.Pointer, oldlenp *uintptr, newp uintptr, newlen uintptr) int32
	fnIfIndexToName    func(ifindex uint32, buf *byte) uintptr
	fnProcListAllPIDs  func(buffer unsafe.Pointer, buffersize int32) int32
	fnProcPIDInfo      func(pid int32, flavor int32, arg uint64, buffer unsafe.Pointer, buffersize int32) int32
	fnProcName         func(pid int32, buffer unsafe.Pointer, buffersize uint32) int32
	fnMachTimebaseInfo func(info unsafe.Pointer) int32

	iokitOnce             sync.Once
	iokitAvailable        bool
	fnIOServiceMatching   func(name *byte) uintptr
	fnIOServiceMatchingSv func(mainPort uintptr, matching uintptr, existing *uint32) int32
	fnIOIteratorNext      func(iterator uint32) uint32
	fnIORegCreateProps    func(entry uint32, props *uintptr, alloc uintptr, opts uint32) int32
	fnIOObjectRelease     func(obj uint32) int32

	fnCFStrCString     func(alloc uintptr, cStr *byte, enc uint32) uintptr
	fnCFDictGetValue   func(dict, key uintptr) uintptr
	fnCFGetTypeID      func(ref uintptr) uintptr
	fnCFDictGetTypeID  func() uintptr
	fnCFNumGetTypeID   func() uintptr
	fnCFNumGetValue    func(num uintptr, theType int64, val unsafe.Pointer) uint8
	fnCFRelease        func(ref uintptr)
	cfKeyStats         uintptr
	cfKeyBytesRead     uintptr
	cfKeyBytesWrite    uintptr
	cStrBlockDriver    = []byte("IOBlockStorageDriver\x00")
	cStrStats          = []byte("Statistics\x00")
	cStrBytesRead      = []byte("Bytes (Read)\x00")
	cStrBytesWrite     = []byte("Bytes (Write)\x00")
)

func loadDarwin() error {
	darwinOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				darwinErr = fmt.Errorf("sysmon: darwin setup failed: %v", r)
			}
		}()
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			darwinErr = err
			return
		}
		purego.RegisterLibFunc(&fnMachHostSelf, lib, "mach_host_self")
		purego.RegisterLibFunc(&fnHostStatistics, lib, "host_statistics")
		purego.RegisterLibFunc(&fnHostStatistics64, lib, "host_statistics64")
		purego.RegisterLibFunc(&fnSysctl, lib, "sysctl")
		purego.RegisterLibFunc(&fnIfIndexToName, lib, "if_indextoname")
		purego.RegisterLibFunc(&fnProcListAllPIDs, lib, "proc_listallpids")
		purego.RegisterLibFunc(&fnProcPIDInfo, lib, "proc_pidinfo")
		purego.RegisterLibFunc(&fnProcName, lib, "proc_name")
		purego.RegisterLibFunc(&fnMachTimebaseInfo, lib, "mach_timebase_info")

		darwinHostPort = fnMachHostSelf()
		darwinPageSize = uint64(unix.Getpagesize())
		if total, err := unix.SysctlUint64("hw.memsize"); err == nil {
			darwinMemTotal = total
		}
		var tb [2]uint32
		if fnMachTimebaseInfo(unsafe.Pointer(&tb[0])) == 0 && tb[1] != 0 {
			darwinTB.numer, darwinTB.denom = tb[0], tb[1]
		} else {
			darwinTB.numer, darwinTB.denom = 1, 1
		}
	})
	return darwinErr
}

func loadDarwinIOKit() bool {
	iokitOnce.Do(func() {
		defer func() {
			if recover() != nil {
				iokitAvailable = false
			}
		}()
		cfLib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		ioLib, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&fnIOServiceMatching, ioLib, "IOServiceMatching")
		purego.RegisterLibFunc(&fnIOServiceMatchingSv, ioLib, "IOServiceGetMatchingServices")
		purego.RegisterLibFunc(&fnIOIteratorNext, ioLib, "IOIteratorNext")
		purego.RegisterLibFunc(&fnIORegCreateProps, ioLib, "IORegistryEntryCreateCFProperties")
		purego.RegisterLibFunc(&fnIOObjectRelease, ioLib, "IOObjectRelease")

		purego.RegisterLibFunc(&fnCFStrCString, cfLib, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&fnCFDictGetValue, cfLib, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&fnCFGetTypeID, cfLib, "CFGetTypeID")
		purego.RegisterLibFunc(&fnCFDictGetTypeID, cfLib, "CFDictionaryGetTypeID")
		purego.RegisterLibFunc(&fnCFNumGetTypeID, cfLib, "CFNumberGetTypeID")
		purego.RegisterLibFunc(&fnCFNumGetValue, cfLib, "CFNumberGetValue")
		purego.RegisterLibFunc(&fnCFRelease, cfLib, "CFRelease")

		cfKeyStats = fnCFStrCString(0, &cStrStats[0], cfStringEncodingUTF8)
		cfKeyBytesRead = fnCFStrCString(0, &cStrBytesRead[0], cfStringEncodingUTF8)
		cfKeyBytesWrite = fnCFStrCString(0, &cStrBytesWrite[0], cfStringEncodingUTF8)
		if cfKeyStats != 0 && cfKeyBytesRead != 0 && cfKeyBytesWrite != 0 {
			iokitAvailable = true
		}
	})
	return iokitAvailable
}

func (darwinReader) ReadCPU() (busy, total uint64, ok bool) {
	if err := loadDarwin(); err != nil || darwinHostPort == 0 {
		return 0, 0, false
	}
	var info [hostCPULoadInfoCount]uint32
	count := uint32(hostCPULoadInfoCount)
	if kr := fnHostStatistics(darwinHostPort, hostCPULoadInfo, unsafe.Pointer(&info[0]), &count); kr != 0 {
		return 0, 0, false
	}
	user := uint64(info[0])
	sys := uint64(info[1])
	idle := uint64(info[2])
	nice := uint64(info[3])
	busy = user + sys + nice
	total = busy + idle
	return busy, total, total > 0
}

func (darwinReader) ReadMem() (total, used, app, wired, comp uint64, ok bool) {
	if err := loadDarwin(); err != nil || darwinHostPort == 0 || darwinMemTotal == 0 {
		return 0, 0, 0, 0, 0, false
	}
	var buf [hostVMInfo64Count * 4]byte
	count := uint32(hostVMInfo64Count)
	if kr := fnHostStatistics64(darwinHostPort, hostVMInfo64, unsafe.Pointer(&buf[0]), &count); kr != 0 {
		return 0, 0, 0, 0, 0, false
	}
	// Offsets verified against macOS Mach vm_statistics64_data_t:
	// wire_count @ 12, purgeable_count @ 88, compressor_page_count @ 128, internal_page_count @ 140.
	wirePages := uint64(binary.LittleEndian.Uint32(buf[12:16]))
	purgeablePages := uint64(binary.LittleEndian.Uint32(buf[88:92]))
	compPages := uint64(binary.LittleEndian.Uint32(buf[128:132]))
	internalPages := uint64(binary.LittleEndian.Uint32(buf[140:144]))

	var appPages uint64
	if internalPages > purgeablePages {
		appPages = internalPages - purgeablePages
	}
	app = appPages * darwinPageSize
	wired = wirePages * darwinPageSize
	comp = compPages * darwinPageSize
	total = darwinMemTotal
	used = min(app+wired+comp, total)
	return total, used, app, wired, comp, true
}

func (darwinReader) ReadNet() (rxBytes, txBytes uint64, ok bool) {
	if err := loadDarwin(); err != nil {
		return 0, 0, false
	}
	mib := [6]int32{ctlNet, pfRoute, 0, 0, netRTIfList2, 0}
	var need uintptr
	if fnSysctl(&mib[0], 6, nil, &need, 0, 0) != 0 || need == 0 {
		return 0, 0, false
	}
	buf := make([]byte, need+256)
	got := uintptr(len(buf))
	if fnSysctl(&mib[0], 6, unsafe.Pointer(&buf[0]), &got, 0, 0) != 0 {
		return 0, 0, false
	}
	buf = buf[:got]
	var ifNameBuf [32]byte
	for off := 0; off+160 <= len(buf); {
		msgLen := int(binary.LittleEndian.Uint16(buf[off : off+2]))
		if msgLen < 160 || off+msgLen > len(buf) {
			break
		}
		if buf[off+3] == rtmIfInfo2 {
			flags := binary.LittleEndian.Uint32(buf[off+8 : off+12])
			if flags&iffUp != 0 && flags&iffLoopback == 0 {
				ifIndex := uint32(binary.LittleEndian.Uint16(buf[off+12 : off+14]))
				ifNameBuf[0] = 0
				if ptr := fnIfIndexToName(ifIndex, &ifNameBuf[0]); ptr != 0 {
					end := bytes.IndexByte(ifNameBuf[:], 0)
					if end < 0 {
						end = len(ifNameBuf)
					}
					ifName := string(ifNameBuf[:end])
					if isPhysicalNetIf(ifName) {
						// if_msghdr2.ifm_data @ 32; if_data64.ifi_ibytes @ +64 (=96), ifi_obytes @ +72 (=104).
						rxBytes += binary.LittleEndian.Uint64(buf[off+96 : off+104])
						txBytes += binary.LittleEndian.Uint64(buf[off+104 : off+112])
					}
				}
			}
		}
		off += msgLen
	}
	return rxBytes, txBytes, true
}

// isPhysicalNetIf filters to external physical/cellular interfaces (en*, pdp_ip*),
// excluding virtual tunnels (utun*), AirDrop/AWDL (awdl*, llw*), Thunderbolt bridges, and loopback.
func isPhysicalNetIf(name string) bool {
	return strings.HasPrefix(name, "en") || strings.HasPrefix(name, "pdp_ip")
}

func (darwinReader) ReadDisk() (total, free, readBytes, writeBytes uint64, directRate, ok bool) {
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil {
		return 0, 0, 0, 0, false, false
	}
	bsize := uint64(st.Bsize)
	total = st.Blocks * bsize
	free = st.Bavail * bsize

	if loadDarwinIOKit() {
		var ok bool
		readBytes, writeBytes, ok = readDarwinBlockStorageStats()
		if !ok {
			// The counters could not be read: say so instead of passing 0
			// off as the count, or the next good sample sees a delta from 0
			// and draws a many-GB/s spike.
			return total, free, 0, 0, false, false
		}
	}
	return total, free, readBytes, writeBytes, false, total > 0
}

func readDarwinBlockStorageStats() (readBytes, writeBytes uint64, ok bool) {
	match := fnIOServiceMatching(&cStrBlockDriver[0])
	if match == 0 {
		return 0, 0, false
	}
	var iter uint32
	if fnIOServiceMatchingSv(0, match, &iter) != 0 || iter == 0 {
		return 0, 0, false
	}
	defer fnIOObjectRelease(iter)

	dictType := fnCFDictGetTypeID()
	numType := fnCFNumGetTypeID()
	for {
		svc := fnIOIteratorNext(iter)
		if svc == 0 {
			break
		}
		var props uintptr
		if fnIORegCreateProps(svc, &props, 0, 0) == 0 && props != 0 {
			stats := fnCFDictGetValue(props, cfKeyStats)
			if stats != 0 && fnCFGetTypeID(stats) == dictType {
				if rNum := fnCFDictGetValue(stats, cfKeyBytesRead); rNum != 0 && fnCFGetTypeID(rNum) == numType {
					var v int64
					if fnCFNumGetValue(rNum, cfNumberSInt64Type, unsafe.Pointer(&v)) != 0 && v > 0 {
						readBytes += uint64(v)
					}
				}
				if wNum := fnCFDictGetValue(stats, cfKeyBytesWrite); wNum != 0 && fnCFGetTypeID(wNum) == numType {
					var v int64
					if fnCFNumGetValue(wNum, cfNumberSInt64Type, unsafe.Pointer(&v)) != 0 && v > 0 {
						writeBytes += uint64(v)
					}
				}
			}
			fnCFRelease(props)
		}
		fnIOObjectRelease(svc)
	}
	return readBytes, writeBytes, true
}

func (darwinReader) ReadProcs() ([]RawProc, bool) {
	if err := loadDarwin(); err != nil {
		return nil, false
	}
	est := fnProcListAllPIDs(nil, 0)
	if est <= 0 {
		return nil, false
	}
	capPIDs := max(int(est)+128, 1024)
	pids := make([]int32, capPIDs)
	got := fnProcListAllPIDs(unsafe.Pointer(&pids[0]), int32(len(pids)*4))
	if got <= 0 {
		return nil, false
	}
	n := min(int(got), len(pids))
	pids = pids[:n]

	var taskBuf [procPIDTaskInfoSize]byte
	var nameBuf [256]byte
	out := make([]RawProc, 0, n)
	numer, denom := uint64(darwinTB.numer), uint64(darwinTB.denom)
	if denom == 0 {
		numer, denom = 1, 1
	}
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if fnProcPIDInfo(pid, procPIDTaskInfo, 0, unsafe.Pointer(&taskBuf[0]), procPIDTaskInfoSize) != procPIDTaskInfoSize {
			continue
		}
		nameLen := fnProcName(pid, unsafe.Pointer(&nameBuf[0]), uint32(len(nameBuf)))
		if nameLen <= 0 {
			continue
		}
		rawName := nameBuf[:min(int(nameLen), len(nameBuf))]
		end := bytes.IndexByte(rawName, 0)
		if end < 0 {
			end = len(rawName)
		}
		if end == 0 {
			continue
		}
		// Offsets verified against sys/proc_info.h struct proc_taskinfo:
		// pti_resident_size @ 8, pti_total_user @ 16, pti_total_system @ 24.
		resBytes := binary.LittleEndian.Uint64(taskBuf[8:16])
		userTicks := binary.LittleEndian.Uint64(taskBuf[16:24])
		sysTicks := binary.LittleEndian.Uint64(taskBuf[24:32])
		ticks := userTicks + sysTicks
		cpuNS := (ticks/denom)*numer + ((ticks%denom)*numer)/denom
		out = append(out, RawProc{
			PID:       pid,
			Name:      string(nameBuf[:end]),
			CPUTimeNS: cpuNS,
			MemBytes:  resBytes,
		})
	}
	return out, len(out) > 0
}
