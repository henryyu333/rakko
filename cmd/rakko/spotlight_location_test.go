package main

import (
	"path/filepath"
	"testing"

	"rakko/internal/platform"
	"rakko/internal/store"
)

func TestSpotHitRowAppLocation(t *testing.T) {
	a := &shelf{store: &store.Store{}, level: 1, visible: true}
	group := a.appLoc().HomeGroupApps()

	// Mac app: folder from Path, reveal present.
	macPath := filepath.Join(string(filepath.Separator), "Applications", "Keynote.app")
	macRow := a.spotHitRow(group, platform.SearchHit{Path: macPath, Name: "Keynote", App: true})
	if macRow.sub != filepath.Dir(macPath) || macRow.reveal == nil {
		t.Fatalf("mac app row = {sub:%q, reveal:%v}, want sub=%q and reveal!=nil",
			macRow.sub, macRow.reveal != nil, filepath.Dir(macPath))
	}

	// Desktop app launched via shell:AppsFolder with resolved TargetPath.
	target := filepath.Join(t.TempDir(), "apps", "chrome.exe")
	deskRow := a.spotHitRow(group, platform.SearchHit{
		Path:       `shell:AppsFolder\Chrome`,
		Name:       "Google Chrome",
		App:        true,
		TargetPath: target,
	})
	if deskRow.sub != filepath.Dir(target) || deskRow.reveal == nil {
		t.Fatalf("desktop app row = {sub:%q, reveal:%v}, want sub=%q and reveal!=nil",
			deskRow.sub, deskRow.reveal != nil, filepath.Dir(target))
	}

	// Microsoft Store / UWP app: localized Store label, no Explorer reveal.
	for _, tc := range []struct {
		locale string
		want   string
	}{
		{"zh-Hans", "Microsoft Store 应用"},
		{"en", "Microsoft Store app"},
	} {
		a.locale = tc.locale
		storeRow := a.spotHitRow(group, platform.SearchHit{
			Path:     `shell:AppsFolder\Microsoft.MicrosoftStickyNotes_8wekyb3d8bbwe!App`,
			Name:     "Sticky Notes",
			App:      true,
			StoreApp: true,
		})
		if storeRow.sub != tc.want || storeRow.reveal != nil {
			t.Fatalf("%s store app row = {sub:%q, reveal:%v}, want sub=%q and reveal==nil",
				tc.locale, storeRow.sub, storeRow.reveal != nil, tc.want)
		}
	}

	// Virtual shell item without resolved target or Store flag stays empty.
	virtRow := a.spotHitRow(group, platform.SearchHit{
		Path: `shell:AppsFolder\UnknownVirtual`,
		Name: "Virtual",
		App:  true,
	})
	if virtRow.sub != "" || virtRow.reveal != nil {
		t.Fatalf("virtual shell row = {sub:%q, reveal:%v}, want empty sub and reveal==nil",
			virtRow.sub, virtRow.reveal != nil)
	}
}
