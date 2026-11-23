//go:build darwin

package platform

import "testing"

// Windows-made zips carry non-UTF-8 names; GB18030 decoding recovers them
// (and reports failure for bytes that are not GB18030 at all).
func TestDecodeGB18030(t *testing.T) {
	if s, ok := DecodeGB18030([]byte("\xb1\xa8\xb1\xed.txt")); !ok || s != "报表.txt" {
		t.Fatalf("decode = %q, %v; want 报表.txt", s, ok)
	}
	if s, ok := DecodeGB18030([]byte("\xca\xfd\xbe\xdd/\xc3\xf7\xcf\xb8.csv")); !ok || s != "数据/明细.csv" {
		t.Fatalf("decode = %q, %v; want 数据/明细.csv", s, ok)
	}
	if _, ok := DecodeGB18030([]byte{0xff, 0xfe, 0xfd}); ok {
		t.Fatal("bytes that are not GB18030 must report failure")
	}
}
