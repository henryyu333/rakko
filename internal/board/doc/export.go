package doc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EmbeddedAsset bundles binary image asset bytes inside a portable .rakkoboard.json file.
type EmbeddedAsset struct {
	Meta *Asset `json:"meta"`
	Data []byte `json:"data"`
}

// BoardPackage is the portable JSON interchange format for .rakkoboard.json files.
type BoardPackage struct {
	FormatVersion int                   `json:"formatVersion"`
	Drawing       *Drawing              `json:"drawing"`
	Assets        map[ID]*EmbeddedAsset `json:"assets,omitempty"`
}

// ExportPackage serializes d and all assets referenced by it into portable .rakkoboard.json bytes.
func ExportPackage(d *Drawing, st *Store) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("board: nil drawing")
	}
	snap := d.Snapshot()
	snap.Normalize(snap.ID, snap.Name)
	pkg := BoardPackage{
		FormatVersion: 1,
		Drawing:       snap,
	}
	if st != nil && len(snap.Assets) > 0 {
		pkg.Assets = make(map[ID]*EmbeddedAsset, len(snap.Assets))
		for id, a := range snap.Assets {
			if a == nil || a.Path == "" {
				continue
			}
			full, err := st.AssetPath(a.Path)
			if err != nil {
				continue
			}
			raw, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			cp := *a
			pkg.Assets[id] = &EmbeddedAsset{Meta: &cp, Data: raw}
		}
	}
	return json.MarshalIndent(pkg, "", "  ")
}

// ImportPackage decodes either a BoardPackage or bare Drawing JSON into st as a new board.
func ImportPackage(raw []byte, st *Store, fallbackName string) (*Drawing, error) {
	if st == nil {
		return nil, fmt.Errorf("board: nil store")
	}
	var pkg BoardPackage
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, err
	}
	var d *Drawing
	if pkg.Drawing != nil {
		d = pkg.Drawing
	} else {
		var bare Drawing
		if err := json.Unmarshal(raw, &bare); err != nil {
			return nil, err
		}
		if len(bare.Pages) == 0 && len(bare.Shapes) == 0 && bare.Name == "" {
			return nil, fmt.Errorf("board: unrecognized board file")
		}
		d = &bare
	}

	newID, err := newBoardID()
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = fallbackName
	}
	d.ID = newID
	d.Normalize(newID, name)

	remap := map[ID]ID{}
	for oldID, emb := range pkg.Assets {
		if emb == nil || len(emb.Data) == 0 {
			continue
		}
		ext := "png"
		nameHint := string(oldID)
		mime := "image/png"
		var w, h float64
		if emb.Meta != nil {
			if e := strings.TrimPrefix(strings.ToLower(filepath.Ext(emb.Meta.Path)), "."); e != "" {
				ext = e
			}
			if emb.Meta.Name != "" {
				nameHint = emb.Meta.Name
			}
			if emb.Meta.Mime != "" {
				mime = emb.Meta.Mime
			}
			w, h = emb.Meta.W, emb.Meta.H
		}
		saved, err := st.SaveAsset(emb.Data, ext, nameHint, mime, w, h)
		if err != nil {
			return nil, err
		}
		d.Assets[saved.ID] = saved
		if saved.ID != oldID {
			delete(d.Assets, oldID)
			remap[oldID] = saved.ID
		}
	}
	if len(remap) > 0 {
		for sid, sh := range d.Shapes {
			if sh == nil || sh.AssetID == "" {
				continue
			}
			if nid, ok := remap[sh.AssetID]; ok {
				cp := sh.Clone()
				cp.AssetID = nid
				d.Shapes[sid] = cp
			}
		}
	}
	if err := st.WriteFor(newID, d); err != nil {
		return nil, err
	}
	return d, nil
}
