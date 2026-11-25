// Package doc defines the whiteboard's immutable document model, fractional
// ordering, multi-page structure, and atomic JSON/asset persistence.
package doc

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"

	"rakko/internal/board/geo"
)

// SchemaVersion is the persisted board JSON format version.
const SchemaVersion = 1

// ID identifies a record in a board document ("shape:...", "page:...", etc.).
type ID string

// Shape types supported by Rakko canvas.
const (
	TypeGeo       = "geo"
	TypeDraw      = "draw"
	TypeHighlight = "highlight"
	TypeArrow     = "arrow"
	TypeLine      = "line"
	TypeText      = "text"
	TypeNote      = "note"
	TypeFrame     = "frame"
	TypeImage     = "image"
	TypeGroup     = "group"
)

// Board appearance, paper, and theme constants.
const (
	LookSketch = "sketch"
	LookClean  = "clean"
	LookMono   = "mono"

	PaperDots  = "dots"
	PaperGrid  = "grid"
	PaperLines = "lines"
	PaperBlank = "blank"

	ThemeDark  = "dark"
	ThemeLight = "light"
)

// PointZ is a freehand stroke point with normalized pressure Z in [0, 1].
type PointZ struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z,omitempty"`
}

// Segment is a freehand or straight run inside a draw/highlight stroke.
type Segment struct {
	Type   string   `json:"type"` // "free" or "straight"
	Points []PointZ `json:"points"`
	Dim    int      `json:"dim,omitempty"` // 2 when pressure is absent
}

// segmentJSON is Segment's compact wire form: points are a flat [x,y(,z)…]
// array rounded to two decimals, which keeps a 1500-stroke board small. The
// reader still accepts the older array of {x,y,z} objects.
type segmentJSON struct {
	Type   string    `json:"type"`
	Dim    int       `json:"dim,omitempty"`
	Points []float64 `json:"points"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// MarshalJSON writes a Segment compactly.
func (s Segment) MarshalJSON() ([]byte, error) {
	dim := s.Dim
	if dim != 2 && dim != 3 {
		dim = 3
	}
	flat := make([]float64, 0, len(s.Points)*dim)
	for _, p := range s.Points {
		flat = append(flat, round2(p.X), round2(p.Y))
		if dim == 3 {
			flat = append(flat, round2(p.Z))
		}
	}
	out := segmentJSON{Type: s.Type, Points: flat}
	if dim == 2 {
		out.Dim = 2
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads both the compact flat form and the old object array.
func (s *Segment) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type   string          `json:"type"`
		Dim    int             `json:"dim"`
		Points json.RawMessage `json:"points"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Type, s.Dim = raw.Type, raw.Dim
	body := bytes.TrimSpace(raw.Points)
	if len(body) == 0 || string(body) == "null" {
		s.Points = nil
		return nil
	}
	inner := body
	if inner[0] == '[' {
		inner = bytes.TrimLeft(inner[1:], " \n\r\t")
	}
	if len(inner) > 0 && inner[0] == '{' {
		var old []PointZ
		if err := json.Unmarshal(body, &old); err != nil {
			return err
		}
		s.Points = old
		return nil
	}
	var flat []float64
	if err := json.Unmarshal(body, &flat); err != nil {
		return err
	}
	dim := 3
	switch {
	case s.Dim == 2 || s.Dim == 3:
		dim = s.Dim
	case len(flat)%3 == 0:
		dim = 3
	case len(flat)%2 == 0:
		dim = 2
	}
	if _, err := parseFlatPoints(flat, dim); err != nil {
		return err
	}
	s.Points = flatPoints(flat, dim)
	return nil
}

func parseFlatPoints(flat []float64, dim int) ([]PointZ, error) {
	if dim != 2 && dim != 3 {
		return nil, fmt.Errorf("board: segment dim %d", dim)
	}
	if len(flat)%dim != 0 {
		return nil, fmt.Errorf("board: %d point values for dim %d", len(flat), dim)
	}
	return flatPoints(flat, dim), nil
}

func flatPoints(flat []float64, dim int) []PointZ {
	out := make([]PointZ, 0, len(flat)/dim)
	for i := 0; i+dim <= len(flat); i += dim {
		p := PointZ{X: flat[i], Y: flat[i+1]}
		if dim == 3 {
			p.Z = flat[i+2]
		}
		out = append(out, p)
	}
	return out
}

// LinePoint is a vertex of a line shape.
type LinePoint struct {
	ID    string  `json:"id"`
	Index string  `json:"index"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// Crop is a non-destructive normalized crop rectangle in [0, 1]^2.
type Crop struct {
	TopLeft     geo.Vec `json:"topLeft"`
	BottomRight geo.Vec `json:"bottomRight"`
	IsCircle    bool    `json:"isCircle,omitempty"`
}

// Shape is an immutable record on a board page. Committed records are never
// mutated in place; edits call Clone() (which clears derived Cache) or Moved()
// (which preserves geometry Cache).
type Shape struct {
	ID       ID      `json:"id"`
	Type     string  `json:"type"`
	Parent   ID      `json:"parent"`
	Index    string  `json:"index"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation,omitempty"`
	Opacity  float64 `json:"opacity"`
	Locked   bool    `json:"locked,omitempty"`

	W      float64 `json:"w,omitempty"`
	H      float64 `json:"h,omitempty"`
	GrowY  float64 `json:"growY,omitempty"`
	Scale  float64 `json:"scale,omitempty"`
	ScaleX float64 `json:"scaleX,omitempty"`
	ScaleY float64 `json:"scaleY,omitempty"`

	// Visual style.
	Geo        string `json:"geo,omitempty"`
	Color      string `json:"color,omitempty"`
	LabelColor string `json:"labelColor,omitempty"`
	Fill       string `json:"fill,omitempty"`
	FillColor  string `json:"fillColor,omitempty"`
	Dash       string `json:"dash,omitempty"`
	Size       string `json:"size,omitempty"`
	Font       string `json:"font,omitempty"`
	FontSize   int    `json:"fontSize,omitempty"`
	Align      string `json:"align,omitempty"`
	VAlign     string `json:"vAlign,omitempty"`
	TextAlign  string `json:"textAlign,omitempty"`
	Bold       bool   `json:"bold,omitempty"`
	Italic     bool   `json:"italic,omitempty"`
	Underline  bool   `json:"underline,omitempty"`
	AutoSize   bool   `json:"autoSize,omitempty"`
	Name       string `json:"name,omitempty"`
	FlipX      bool   `json:"flipX,omitempty"`
	FlipY      bool   `json:"flipY,omitempty"`
	Seed       uint32 `json:"seed,omitempty"`

	// Text / label content.
	Text string `json:"text,omitempty"`

	// Freehand draw / highlight / curve properties.
	Segments   []Segment `json:"segments,omitempty"`
	IsComplete bool      `json:"isComplete,omitempty"`
	IsClosed   bool      `json:"isClosed,omitempty"`
	IsPen      bool      `json:"isPen,omitempty"`
	PenKind    string    `json:"penKind,omitempty"`
	PenWidth   float64   `json:"penWidth,omitempty"`

	// Arrow properties.
	Kind          string  `json:"kind,omitempty"` // "arc" (straight/curved) or "elbow"
	HeadStart     string  `json:"headStart,omitempty"`
	HeadEnd       string  `json:"headEnd,omitempty"`
	Start         geo.Vec `json:"start,omitempty"`
	End           geo.Vec `json:"end,omitempty"`
	Bend          float64 `json:"bend,omitempty"`
	LabelPosition float64 `json:"labelPosition,omitempty"`
	ElbowMidPoint float64 `json:"elbowMidPoint,omitempty"`

	// Line / polygon vertices.
	Spline string      `json:"spline,omitempty"` // "line" or "cubic"
	Points []LinePoint `json:"points,omitempty"`

	// Image properties.
	AssetID ID    `json:"assetId,omitempty"`
	Crop    *Crop `json:"crop,omitempty"`

	// Cache stores derived geometry/display data on the UI thread; never serialized.
	Cache any `json:"-"`
}

// Clone returns an editable shallow copy of s with its derived Cache cleared.
func (s *Shape) Clone() *Shape {
	c := *s
	c.Cache = nil
	return &c
}

// Moved returns an editable copy of s preserving derived Cache, valid when only
// X, Y, Rotation, Parent, or Index changes.
func (s *Shape) Moved() *Shape {
	c := *s
	return &c
}

// Binding attaches an arrow terminal ("start" or "end") to a target shape.
type Binding struct {
	ID        ID      `json:"id"`
	From      ID      `json:"from"`
	To        ID      `json:"to"`
	Terminal  string  `json:"terminal"` // "start" or "end"
	Anchor    geo.Vec `json:"anchor"`
	IsExact   bool    `json:"isExact,omitempty"`
	IsPrecise bool    `json:"isPrecise,omitempty"`
	Snap      string  `json:"snap,omitempty"`
}

// PageCamera remembers a page's last viewport state.
type PageCamera struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Page is one page inside a whiteboard document.
type Page struct {
	ID     ID          `json:"id"`
	Name   string      `json:"name"`
	Index  string      `json:"index"`
	Camera *PageCamera `json:"camera,omitempty"`
}

// Asset describes an embedded image file stored content-addressed in boards/assets/.
type Asset struct {
	ID   ID      `json:"id"`
	Type string  `json:"type"` // "image"
	W    float64 `json:"w"`
	H    float64 `json:"h"`
	Name string  `json:"name,omitempty"`
	Mime string  `json:"mime,omitempty"`
	Hash string  `json:"hash,omitempty"`
	Path string  `json:"path"` // relative to boards/, e.g. "assets/<hash>.png"
}

// Drawing is a complete multi-page whiteboard document.
type Drawing struct {
	Version    int             `json:"version"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Look       string          `json:"look,omitempty"`
	Paper      string          `json:"paper,omitempty"`
	Theme      string          `json:"theme,omitempty"`
	GridSize   float64         `json:"gridSize,omitempty"`
	ActivePage ID              `json:"activePage,omitempty"`
	Pages      []*Page         `json:"pages"`
	Shapes     map[ID]*Shape   `json:"shapes,omitempty"`
	Bindings   map[ID]*Binding `json:"bindings,omitempty"`
	Assets     map[ID]*Asset   `json:"assets,omitempty"`
}

// ErrLastPage is returned when attempting to delete the only remaining page.
var ErrLastPage = errors.New("board: cannot delete the last page")

// NewDrawing creates a fresh single-page drawing with default settings.
func NewDrawing(id, name string) *Drawing {
	pageID := ID("page:1")
	return &Drawing{
		Version:    SchemaVersion,
		ID:         id,
		Name:       name,
		Look:       LookSketch,
		Paper:      PaperDots,
		Theme:      ThemeDark,
		GridSize:   40,
		ActivePage: pageID,
		Pages:      []*Page{{ID: pageID, Name: "Page 1", Index: FirstIndex}},
		Shapes:     map[ID]*Shape{},
		Bindings:   map[ID]*Binding{},
		Assets:     map[ID]*Asset{},
	}
}

// Normalize ensures maps, defaults, and at least one valid page exist after decoding.
func (d *Drawing) Normalize(fallbackID, fallbackName string) {
	if d.Version == 0 {
		d.Version = SchemaVersion
	}
	if d.ID == "" {
		d.ID = fallbackID
	}
	if d.Name == "" {
		d.Name = fallbackName
	}
	switch d.Look {
	case LookSketch, LookClean, LookMono:
	default:
		d.Look = LookSketch
	}
	switch d.Paper {
	case PaperDots, PaperGrid, PaperLines, PaperBlank:
	default:
		d.Paper = PaperDots
	}
	switch d.Theme {
	case ThemeDark, ThemeLight:
	default:
		d.Theme = ThemeDark
	}
	if d.GridSize <= 0 {
		d.GridSize = 40
	}
	if len(d.Pages) == 0 {
		d.Pages = []*Page{{ID: "page:1", Name: "Page 1", Index: FirstIndex}}
	}
	if d.Page(d.ActivePage) == nil {
		d.ActivePage = d.Pages[0].ID
	}
	if d.Shapes == nil {
		d.Shapes = map[ID]*Shape{}
	}
	if d.Bindings == nil {
		d.Bindings = map[ID]*Binding{}
	}
	if d.Assets == nil {
		d.Assets = map[ID]*Asset{}
	}
	d.pruneOrphans()
	d.renumberDuplicateIndices()
}

// pruneOrphans drops shapes whose ancestor chain does not reach an existing page,
// and bindings whose endpoints no longer exist.
func (d *Drawing) pruneOrphans() {
	pages := make(map[ID]bool, len(d.Pages))
	for _, p := range d.Pages {
		if p != nil {
			pages[p.ID] = true
		}
	}
	for id, s := range d.Shapes {
		if s == nil {
			delete(d.Shapes, id)
			continue
		}
		cur := s.Parent
		ok := false
		for steps := 0; steps <= len(d.Shapes); steps++ {
			if pages[cur] {
				ok = true
				break
			}
			p := d.Shapes[cur]
			if p == nil {
				break
			}
			cur = p.Parent
		}
		if !ok {
			delete(d.Shapes, id)
		}
	}
	for id, b := range d.Bindings {
		if b == nil || d.Shapes[b.From] == nil || d.Shapes[b.To] == nil {
			delete(d.Bindings, id)
		}
	}
}

// renumberDuplicateIndices restores a strict z-order when imported JSON reuses
// fractional index keys under one parent, keeping the decoded order stable.
func (d *Drawing) renumberDuplicateIndices() {
	groups := make(map[ID][]ID, 8)
	for id, s := range d.Shapes {
		groups[s.Parent] = append(groups[s.Parent], id)
	}
	for _, ids := range groups {
		if len(ids) < 2 {
			continue
		}
		sort.SliceStable(ids, func(i, j int) bool {
			a, b := d.Shapes[ids[i]], d.Shapes[ids[j]]
			if a.Index != b.Index {
				return a.Index < b.Index
			}
			return ids[i] < ids[j]
		})
		dup := false
		for i := 1; i < len(ids); i++ {
			if d.Shapes[ids[i]].Index == d.Shapes[ids[i-1]].Index {
				dup = true
				break
			}
		}
		if !dup {
			continue
		}
		cur := ""
		for _, id := range ids {
			cur = IndexBetween(cur, "")
			s := d.Shapes[id]
			if s.Index == cur {
				continue
			}
			c := s.Clone()
			c.Index = cur
			d.Shapes[id] = c
		}
	}
}

// PruneUnusedAssets deletes entries in Assets that are no longer referenced by
// any shape in Shapes. Keeping this out of Normalize lets in-session Undo still
// restore a deleted image before the drawing is exported or reopened from disk.
func (d *Drawing) PruneUnusedAssets() {
	if len(d.Assets) == 0 {
		return
	}
	referenced := make(map[ID]bool, len(d.Assets))
	for _, s := range d.Shapes {
		if s != nil && s.AssetID != "" {
			referenced[s.AssetID] = true
		}
	}
	for id := range d.Assets {
		if !referenced[id] {
			delete(d.Assets, id)
		}
	}
}

// Snapshot returns a cheap shallow copy whose maps, slices, and page records
// are independent of future edits.
func (d *Drawing) Snapshot() *Drawing {
	c := *d
	pages := make([]*Page, len(d.Pages))
	for i, p := range d.Pages {
		if p == nil {
			continue
		}
		cp := *p
		if p.Camera != nil {
			cam := *p.Camera
			cp.Camera = &cam
		}
		pages[i] = &cp
	}
	c.Pages = pages
	c.Shapes = maps.Clone(d.Shapes)
	c.Bindings = maps.Clone(d.Bindings)
	c.Assets = maps.Clone(d.Assets)
	return &c
}

// SnapshotUncached returns an independent Snapshot whose Shape records are
// shallow-cloned with cleared render caches, safe for off-thread rendering.
func (d *Drawing) SnapshotUncached() *Drawing {
	c := d.Snapshot()
	for id, s := range c.Shapes {
		if s != nil {
			c.Shapes[id] = s.Clone()
		}
	}
	return c
}

// Page returns the page with id, or nil if not found.
func (d *Drawing) Page(id ID) *Page {
	for _, p := range d.Pages {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// IsPageID reports whether id has the "page:" prefix.
func IsPageID(id ID) bool {
	return len(id) > 5 && id[:5] == "page:"
}

// AddPage appends a new page after the last existing page and returns it.
func (d *Drawing) AddPage(name string) *Page {
	lastIdx := ""
	if n := len(d.Pages); n > 0 {
		lastIdx = d.Pages[n-1].Index
	}
	p := &Page{
		ID:    NewRecordID("page"),
		Name:  name,
		Index: IndexBetween(lastIdx, ""),
	}
	if p.Name == "" {
		p.Name = fmt.Sprintf("Page %d", len(d.Pages)+1)
	}
	d.Pages = append(d.Pages, p)
	return p
}

// RenamePage renames page id in-place on a new Page record.
func (d *Drawing) RenamePage(id ID, name string) bool {
	for i, p := range d.Pages {
		if p.ID == id {
			cp := *p
			cp.Name = name
			pages := slices.Clone(d.Pages)
			pages[i] = &cp
			d.Pages = pages
			return true
		}
	}
	return false
}

// SetPageCamera updates page id's remembered camera on a cloned Page record so
// concurrent Snapshot readers never race with viewport pan or zoom.
func (d *Drawing) SetPageCamera(id ID, cam PageCamera) bool {
	for i, p := range d.Pages {
		if p.ID == id {
			if p.Camera != nil && *p.Camera == cam {
				return true
			}
			cp := *p
			cc := cam
			cp.Camera = &cc
			pages := slices.Clone(d.Pages)
			pages[i] = &cp
			d.Pages = pages
			return true
		}
	}
	return false
}

// DeletePage removes page id unless it is the last remaining page.
func (d *Drawing) DeletePage(id ID) error {
	if len(d.Pages) <= 1 {
		return ErrLastPage
	}
	idx := -1
	for i, p := range d.Pages {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("board: page %q not found", id)
	}
	pages := slices.Clone(d.Pages)
	pages = slices.Delete(pages, idx, idx+1)
	d.Pages = pages
	if d.ActivePage == id {
		next := idx
		if next >= len(d.Pages) {
			next = len(d.Pages) - 1
		}
		d.ActivePage = d.Pages[next].ID
	}
	return nil
}

// NewShape returns a new Shape with sensible defaults for typ.
func NewShape(id ID, typ string) *Shape {
	s := &Shape{
		ID:      id,
		Type:    typ,
		Opacity: 1,
		Scale:   1,
		Index:   FirstIndex,
		Color:   "#E8E8EA",
		Fill:    "none",
		Dash:    "solid",
		Size:    "m",
		Font:    "sketch",
		Align:   "center",
		VAlign:  "middle",
	}
	switch typ {
	case TypeGeo:
		s.Geo, s.W, s.H = "rectangle", 120, 80
	case TypeArrow:
		s.Kind, s.HeadStart, s.HeadEnd = "arc", "none", "arrow"
		s.LabelPosition, s.ElbowMidPoint = 0.5, 0.5
	case TypeLine:
		s.Spline = "line"
	case TypeText:
		s.TextAlign, s.FontSize, s.AutoSize = "left", 18, true
	case TypeNote:
		s.W, s.H, s.FontSize, s.Align, s.VAlign = 180, 140, 16, "left", "top"
	case TypeFrame:
		s.W, s.H, s.Name = 320, 180, "Frame"
	case TypeImage:
		s.W, s.H = 100, 100
	}
	return s
}

// NewRecordID returns a random ID with prefix ("shape:...", "page:...", etc.).
func NewRecordID(prefix string) ID {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ID(prefix + ":0")
	}
	return ID(prefix + ":" + hex.EncodeToString(b[:]))
}
