//go:build darwin

package platform

// Legacy charset decoding for archive member names: zip files made on
// non-UTF-8 systems (a Windows machine zips Chinese names as GBK bytes
// with no UTF-8 flag) carry raw byte names that bsdtar, ditto and unzip
// all write verbatim — as CP437-mojibake. Decoding those bytes as
// GB18030 (a superset of GBK) through CFString recovers the real names.

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	cfEncToNSStringEnc func(cfEnc uint32) uint32
	charsetOnce        sync.Once
	charsetErr         error
)

// loadCharset resolves CFStringConvertEncodingToNSStringEncoding once.
func loadCharset() error {
	if err := Init(); err != nil {
		return err
	}
	charsetOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				charsetErr = fmt.Errorf("mac: charset setup failed: %v", r)
			}
		}()
		libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			charsetErr = fmt.Errorf("mac: cannot load CoreFoundation: %w", err)
			return
		}
		purego.RegisterLibFunc(&cfEncToNSStringEnc, libCF, "CFStringConvertEncodingToNSStringEncoding")
	})
	return charsetErr
}

// DecodeGB18030 decodes b as GB18030 (GBK superset). The result reports
// whether every byte decoded — false when the input is not GB18030 at all
// — so callers can fall back to the raw name.
func DecodeGB18030(b []byte) (s string, ok bool) {
	if err := loadCharset(); err != nil {
		return "", false
	}
	// kCFStringEncodingGB_18030_2000.
	const cfGB18030 = 0x0632
	enc := cfEncToNSStringEnc(cfGB18030)
	withPool(func() {
		str := send(send(class("NSString"), "alloc"), "initWithBytes:length:encoding:",
			uintptr(unsafe.Pointer(unsafe.SliceData(b))), uintptr(len(b)), uintptr(enc))
		if str == 0 {
			return
		}
		s = goString(str)
		send(str, "autorelease") // the pool owns it now
		ok = true
	})
	return s, ok
}
