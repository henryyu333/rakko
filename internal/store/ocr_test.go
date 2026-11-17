package store

import (
	"bytes"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// TestSetOCRPersistsAndReloads proves recognized text written onto an
// image item survives a close/open cycle.
func TestSetOCRPersistsAndReloads(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddClipImage(makePNG(t, 8, 8, color.RGBA{R: 120, A: 255}), "com.example.App"); err != nil {
		t.Fatal(err)
	}
	id := s.Items[0].ID
	if s.Items[0].OCRDone || s.Items[0].OCRText != "" {
		t.Fatalf("new image must not start recognized: %+v", s.Items[0])
	}
	if err := s.SetOCR(id, "货架 Rakko"); err != nil {
		t.Fatalf("SetOCR: %v", err)
	}
	if s.Items[0].OCRText != "货架 Rakko" || !s.Items[0].OCRDone {
		t.Fatalf("SetOCR did not mark the item: %+v", s.Items[0])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Items[0].OCRText != "货架 Rakko" || !s2.Items[0].OCRDone {
		t.Fatalf("reloaded item lost OCR: %+v", s2.Items[0])
	}
}

// TestSetOCRNoText proves a recognition that found nothing is still
// persisted as done, so it is never retried.
func TestSetOCRNoText(t *testing.T) {
	s, _ := newStore(t)
	if err := s.AddImage(makePNG(t, 8, 8, color.RGBA{R: 120, A: 255})); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOCR(s.Items[0].ID, ""); err != nil {
		t.Fatal(err)
	}
	if !s.Items[0].OCRDone || s.Items[0].OCRText != "" {
		t.Fatalf("empty recognition must still be done: %+v", s.Items[0])
	}
	raw, err := os.ReadFile(filepath.Join(s.Root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"ocrDone": true`)) {
		t.Fatalf("state.json lacks the done marker: %s", raw)
	}
}

// TestSetOCRUnknownID is the deleted-while-recognizing path: the write
// must fail and must not resurrect or corrupt the item list.
func TestSetOCRUnknownID(t *testing.T) {
	s, _ := newStore(t)
	if err := s.AddClipImage(makePNG(t, 8, 8, color.RGBA{R: 120, A: 255}), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(s.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOCR("gone-id", "text"); err == nil {
		t.Fatal("SetOCR on a removed item must fail")
	}
	if len(s.Items) != 0 {
		t.Fatalf("failed SetOCR must not add items: %+v", s.Items)
	}
}

// TestLegacyStateLoadsWithoutOCR proves a state.json written before the
// OCR fields existed loads unchanged: no done flag, no text, other fields
// intact.
func TestLegacyStateLoadsWithoutOCR(t *testing.T) {
	legacy := `{"version":1,"items":[{"id":"i1","kind":"text","text":"老记录","createdAt":"2026-10-01T10:00:00Z","source":"clipboard","sourceApp":"com.apple.Notes","pinned":true}]}`
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.statePath(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("legacy state must load: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 1 {
		t.Fatalf("legacy items dropped: %+v", s2.Items)
	}
	it := s2.Items[0]
	if !it.Pinned || it.SourceApp != "com.apple.Notes" || it.OCRDone || it.OCRText != "" {
		t.Fatalf("legacy item misread: %+v", it)
	}
	// The state file keeps its shape: a re-save writes the new keys only
	// for items that actually used them (omitempty).
	if err := s2.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s2.statePath())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(decoded["items"], &items); err != nil {
		t.Fatal(err)
	}
	if _, ok := items[0]["ocrDone"]; ok {
		t.Fatalf("unrecognized items must not grow an ocrDone key: %s", items[0])
	}
}
