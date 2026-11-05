//go:build darwin

package netproxy

import "testing"

func TestAcceptanceProxyNumericEnable(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatal(err)
	}
	dict := cfDict(t, map[string]any{
		"HTTPEnable": 1,
		"HTTPProxy":  "127.0.0.1",
		"HTTPPort":   7890,
	})
	defer cfReleaseSys(dict)
	got := parseProxies(dict)
	if got.HTTP != "127.0.0.1:7890" {
		t.Fatalf("documented CFNumber HTTPEnable=1 ignored: HTTP=%q", got.HTTP)
	}
}
