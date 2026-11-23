// Package doc defines the whiteboard's immutable document model, fractional
// ordering, multi-page structure, and atomic JSON/asset persistence.
package doc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"

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
}

// Snapshot returns a cheap shallow copy whose maps/slices are independent of future edits.
func (d *Drawing) Snapshot() *Drawing {
	c := *d
	c.Pages = slices.Clone(d.Pages)
	c.Shapes = maps.Clone(d.Shapes)
	c.Bindings = maps.Clone(d.Bindings)
	c.Assets = maps.Clone(d.Assets)
	return &c
}

// Equal reports whether two snapshots hold identical document metadata and record pointers.
func Equal(a, b *Drawing) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Version != b.Version || a.ID != b.ID || a.Name != b.Name ||
		a.Look != b.Look || a.Paper != b.Paper || a.Theme != b.Theme ||
		a.GridSize != b.GridSize || a.ActivePage != b.ActivePage {
		return false
	}
	if !slices.Equal(a.Pages, b.Pages) {
		return false
	}
	return maps.Equal(a.Shapes, b.Shapes) &&
		maps.Equal(a.Bindings, b.Bindings) &&
		maps.Equal(a.Assets, b.Assets)
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
