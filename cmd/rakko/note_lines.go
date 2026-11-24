package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui/richtext"
)

// noteLayout is a note file as the editor sees it: one line of the file is one
// paragraph, so a note typed one line at a time keeps its hard line breaks.
// Each unit remembers the exact source bytes it came from; a save writes that
// back verbatim for every line the user did not touch, so editing one line
// never rewrites its neighbours.
type noteLayout struct {
	src   string
	units []noteUnit
	nl    string // "\n" or "\r\n", used for paragraphs typed after loading
	tail  string // terminator new lines end with: nl when the file had one
}

type noteUnitKind int

const (
	noteUnitLine noteUnitKind = iota
	noteUnitBlank
	noteUnitCode
	noteUnitTableHead
	noteUnitTableRow
)

type noteUnit struct {
	kind  noteUnitKind
	raw   string // exact source text, terminator included
	paras []richtext.Paragraph

	// code
	open, close string
	hasClose    bool

	// table head: the separator row between the raw header and the body
	sep string
}

// splitNoteTerm splits raw into content and its final "\r\n", "\n", or "".
func splitNoteTerm(raw string) (string, string) {
	switch {
	case strings.HasSuffix(raw, "\r\n"):
		return raw[:len(raw)-2], "\r\n"
	case strings.HasSuffix(raw, "\n"):
		return raw[:len(raw)-1], "\n"
	}
	return raw, ""
}

// noteSrcLine is one physical line of the source.
type noteSrcLine struct {
	text string // without the terminator
	term string
	raw  string
	off  int // byte offset in src
}

func splitNoteLines(src string) []noteSrcLine {
	var out []noteSrcLine
	for off := 0; off < len(src); {
		i := strings.IndexByte(src[off:], '\n')
		if i < 0 {
			out = append(out, noteSrcLine{text: src[off:], raw: src[off:], off: off})
			break
		}
		raw := src[off : off+i+1]
		text := raw[:len(raw)-1]
		term := "\n"
		if strings.HasSuffix(text, "\r") {
			text = text[:len(text)-1]
			term = "\r\n"
		}
		out = append(out, noteSrcLine{text: text, term: term, raw: raw, off: off})
		off += i + 1
	}
	return out
}

// newNoteLayout splits src into line units, fenced code blocks, and tables.
func newNoteLayout(src string) *noteLayout {
	l := &noteLayout{src: src, nl: "\n"}
	lines := splitNoteLines(src)
	for _, ln := range lines {
		if ln.term == "\r\n" {
			l.nl = "\r\n"
			break
		}
	}
	if strings.HasSuffix(src, "\n") {
		l.tail = l.nl
	}
	for i := 0; i < len(lines); {
		ln := lines[i]
		if marker, ok := noteFence(ln.text); ok {
			j := i + 1
			for ; j < len(lines); j++ {
				if noteFenceClose(lines[j].text, marker) {
					break
				}
			}
			u := noteUnit{kind: noteUnitCode, open: ln.text, hasClose: j < len(lines)}
			end := i
			if u.hasClose {
				u.close = lines[j].text
				end = j
			} else {
				end = len(lines) - 1
			}
			u.raw = src[lines[i].off : lines[end].off+len(lines[end].raw)]
			for k := i + 1; k < end || (k == end && !u.hasClose && k < len(lines)); k++ {
				u.paras = append(u.paras, noteCodePara(lines[k].text))
			}
			if len(u.paras) == 0 {
				u.paras = []richtext.Paragraph{noteCodePara("")}
			}
			l.units = append(l.units, u)
			i = end + 1
			continue
		}
		if i+1 < len(lines) && strings.Contains(ln.text, "|") && noteTableSep(lines[i+1].text) {
			end := i + 2
			for end < len(lines) && strings.Contains(lines[end].text, "|") && strings.TrimSpace(lines[end].text) != "" && !noteFenceStart(lines[end].text) {
				end++
			}
			headRaw := src[lines[i].off : lines[i+1].off+len(lines[i+1].raw)]
			l.units = append(l.units, noteUnit{
				kind:  noteUnitTableHead,
				raw:   headRaw,
				paras: []richtext.Paragraph{noteTablePara(ln.text, true)},
				sep:   lines[i+1].text,
			})
			for k := i + 2; k < end; k++ {
				l.units = append(l.units, noteUnit{
					kind:  noteUnitTableRow,
					raw:   lines[k].raw,
					paras: []richtext.Paragraph{noteTablePara(lines[k].text, false)},
				})
			}
			i = end
			continue
		}
		if strings.TrimSpace(ln.text) == "" {
			l.units = append(l.units, noteUnit{kind: noteUnitBlank, raw: ln.raw, paras: []richtext.Paragraph{{}}})
			i++
			continue
		}
		l.units = append(l.units, noteUnit{
			kind:  noteUnitLine,
			raw:   ln.raw,
			paras: parseNoteLine(ln.text),
		})
		i++
	}
	l.mergeRuns()
	return l
}

// mergeRuns rewrites each unit's paragraphs through TextOf, whose run merging
// is what Document.Text() returns; without it a rebuilt table row would never
// compare equal to the paragraph the editor holds.
func (l *noteLayout) mergeRuns() {
	var all []richtext.Paragraph
	for _, u := range l.units {
		all = append(all, u.paras...)
	}
	if len(all) == 0 {
		return
	}
	var flat []richtext.Paragraph
	for _, p := range richtext.TextOf(all...).Paragraphs() {
		flat = append(flat, p)
	}
	idx := 0
	for i := range l.units {
		n := len(l.units[i].paras)
		if idx+n > len(flat) {
			break
		}
		l.units[i].paras = flat[idx : idx+n]
		idx += n
	}
}

func noteFenceStart(s string) bool {
	_, ok := noteFence(s)
	return ok
}

// noteFence reports the fence marker (``` or ~~~, at least 3) and its indent.
func noteFence(s string) (string, bool) {
	t := strings.TrimLeft(s, " \t")
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return t[:n], true
		}
	}
	return "", false
}

func noteFenceClose(line, openMarker string) bool {
	marker, ok := noteFence(line)
	if !ok || marker[0] != openMarker[0] || len(marker) < len(openMarker) {
		return false
	}
	rest := strings.TrimLeft(line, " \t")[len(marker):]
	return strings.TrimSpace(rest) == ""
}

func noteCodePara(s string) richtext.Paragraph {
	p := richtext.Paragraph{Style: richtext.ParagraphStyle{CodeBlock: true}}
	if s != "" {
		p.Runs = []richtext.Run{{Text: s}}
	}
	return p
}

// escapeNoteHTML keeps literal angle brackets as text: the Markdown reader
// drops unknown HTML tags, which would otherwise delete <bar> from a note.
func escapeNoteHTML(s string) string {
	return strings.ReplaceAll(s, "<", `\<`)
}

func parseNoteLine(s string) []richtext.Paragraph {
	var out []richtext.Paragraph
	for _, p := range richtext.ParseMarkdown(escapeNoteHTML(s)).Paragraphs() {
		out = append(out, p)
	}
	if len(out) == 0 {
		out = []richtext.Paragraph{{Runs: []richtext.Run{{Text: s}}}}
	}
	return out
}

func noteTableSep(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || !strings.Contains(t, "-") {
		return false
	}
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	if t == "" {
		return false
	}
	for _, c := range strings.Split(t, "|") {
		c = strings.TrimSpace(c)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func noteTablePara(s string, header bool) richtext.Paragraph {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	var p richtext.Paragraph
	for i, cell := range strings.Split(t, "|") {
		if i > 0 {
			p.Runs = append(p.Runs, richtext.Run{Text: "\t"})
		}
		paras := parseNoteLine(strings.TrimSpace(cell))
		runs := paras[0].Runs
		if header {
			for k := range runs {
				runs[k].Attributes.Weight = 700
			}
		}
		p.Runs = append(p.Runs, runs...)
	}
	return p
}

// text returns the editor text of the layout: one paragraph per source line.
func (l *noteLayout) text() richtext.Text {
	var all []richtext.Paragraph
	for _, u := range l.units {
		all = append(all, u.paras...)
	}
	if len(all) == 0 {
		return richtext.Text{}
	}
	return richtext.TextOf(all...)
}

// commit serializes the edited text back into note source, reusing the original
// bytes of every unit the user did not change. It reports false when the result
// cannot be read back as the same visible text, in which case the caller must
// not write.
func (l *noteLayout) commit(cur richtext.Text) (string, bool) {
	var curParas []richtext.Paragraph
	for _, p := range cur.Paragraphs() {
		curParas = append(curParas, p)
	}
	if l == nil {
		return strings.Join(notePlainParas(curParas), "\n"), true
	}
	origKeys := make([]string, 0, len(l.units))
	starts := make([]int, len(l.units))
	ends := make([]int, len(l.units))
	for i, u := range l.units {
		starts[i] = len(origKeys)
		for _, p := range u.paras {
			origKeys = append(origKeys, noteParaKey(p))
		}
		ends[i] = len(origKeys)
	}
	positions := make(map[string][]int, len(origKeys))
	for i, key := range origKeys {
		positions[key] = append(positions[key], i)
	}
	curKeys := make([]string, len(curParas))
	for j, p := range curParas {
		curKeys[j] = noteParaKey(p)
	}
	matchOrig := make([]int, len(origKeys))
	matchCur := make([]int, len(curParas))
	for i := range matchOrig {
		matchOrig[i] = -1
	}
	for j := range matchCur {
		matchCur[j] = -1
	}
	last := -1
	for j, key := range curKeys {
		list := positions[key]
		k := sort.SearchInts(list, last+1)
		if k < len(list) {
			i := list[k]
			matchOrig[i] = j
			matchCur[j] = i
			last = i
		}
	}
	var pieces []notePiece
	pos := 0
	for u := range l.units {
		unit := l.units[u]
		var ms []int
		for i := starts[u]; i < ends[u]; i++ {
			if matchOrig[i] >= 0 {
				ms = append(ms, matchOrig[i])
			}
		}
		if len(ms) == 0 {
			// The unit's own text changed beyond recognition, or it was
			// deleted; if new paragraphs sit before the next matched unit,
			// they are its replacement and keep its shape (a table row stays
			// a row, a code line stays code).
			next := l.nextMatch(u+1, matchOrig, starts, ends, len(curParas))
			if pos < next {
				pieces = append(pieces, l.serializeUnit(unit, curParas[pos:next])...)
				pos = next
			}
			continue
		}
		first := ms[0]
		if first > pos {
			pieces = append(pieces, notePieces(curParas[pos:first], false, l.nl)...)
			pos = first
		}
		limit := len(curParas)
		for v := u + 1; v < len(l.units); v++ {
			for i := starts[v]; i < ends[v]; i++ {
				if matchOrig[i] >= 0 {
					limit = matchOrig[i]
					break
				}
			}
			if limit < len(curParas) {
				break
			}
		}
		if lastMatch := ms[len(ms)-1] + 1; limit < lastMatch {
			limit = lastMatch
		}
		unchanged := len(ms) == ends[u]-starts[u]
		if unchanged {
			for k := 0; k < len(ms); k++ {
				if matchOrig[starts[u]+k] != first+k {
					unchanged = false
					break
				}
			}
		}
		if unchanged {
			content, term := splitNoteTerm(unit.raw)
			pieces = append(pieces, notePiece{content: content, term: term})
			pos = first + ends[u] - starts[u]
			continue
		}
		pieces = append(pieces, l.serializeUnit(unit, curParas[first:limit])...)
		pos = limit
	}
	if pos < len(curParas) {
		pieces = append(pieces, notePieces(curParas[pos:], false, l.nl)...)
		if n := len(pieces); n > 0 {
			pieces[n-1].term = l.tail
		}
	}
	var b strings.Builder
	for i, p := range pieces {
		b.WriteString(p.content)
		term := p.term
		if term == "" && i < len(pieces)-1 {
			term = l.nl
		}
		b.WriteString(term)
	}
	out := b.String()
	// Round-trip guard: what the file would hold must read back as the same
	// visible text, or the caller blocks the write and goes read-only.
	if newNoteLayout(out).text().String() != cur.String() {
		return "", false
	}
	return out, true
}

// nextMatch returns the current-paragraph position of the first match at or
// after unit v, or fallback when none follows.
func (l *noteLayout) nextMatch(v int, matchOrig, starts, ends []int, fallback int) int {
	for ; v < len(l.units); v++ {
		for i := starts[v]; i < ends[v]; i++ {
			if matchOrig[i] >= 0 {
				return matchOrig[i]
			}
		}
	}
	return fallback
}

type notePiece struct {
	content, term string
}

func notePieces(paras []richtext.Paragraph, code bool, nl string) []notePiece {
	var out []notePiece
	for _, p := range paras {
		out = append(out, notePiece{content: noteSerializePara(p, code), term: nl})
	}
	return out
}

func notePlainParas(paras []richtext.Paragraph) []string {
	out := make([]string, len(paras))
	for i, p := range paras {
		out[i] = noteSerializePara(p, false)
	}
	return out
}

// noteSerializePara writes one paragraph. A plain body paragraph is written as
// typed (no Markdown escaping), so "!" and "<tag>" stay as the user typed them;
// anything with a block style or run formatting is written as Markdown.
func noteSerializePara(p richtext.Paragraph, code bool) string {
	var b strings.Builder
	if code || p.Style.CodeBlock {
		for _, r := range p.Runs {
			b.WriteString(r.Text)
		}
		return b.String()
	}
	plain := p.Style == (richtext.ParagraphStyle{})
	for _, r := range p.Runs {
		if r.Attributes != (richtext.Attributes{}) {
			plain = false
			break
		}
	}
	if plain {
		for _, r := range p.Runs {
			b.WriteString(r.Text)
		}
		return b.String()
	}
	return richtext.TextOf(p).Markdown()
}

func (l *noteLayout) serializeUnit(u noteUnit, cur []richtext.Paragraph) []notePiece {
	_, term := splitNoteTerm(u.raw)
	if term == "" {
		term = l.nl
	}
	switch u.kind {
	case noteUnitCode:
		var lines []string
		for _, p := range cur {
			lines = append(lines, noteSerializePara(p, true))
		}
		if len(lines) == 0 {
			lines = []string{""}
		}
		body := u.open + "\n" + strings.Join(lines, "\n")
		if u.hasClose {
			body += "\n" + u.close
		}
		return []notePiece{{content: body, term: term}}
	case noteUnitTableHead:
		head := ""
		if len(cur) > 0 {
			head = noteSerializeTableRow(cur[0], u.raw)
		}
		body := head + "\n" + u.sep
		out := []notePiece{{content: body, term: term}}
		out = append(out, notePieces(cur[1:], false, l.nl)...)
		return out
	case noteUnitTableRow:
		if len(cur) == 0 {
			return nil
		}
		out := []notePiece{{content: noteSerializeTableRow(cur[0], u.raw), term: term}}
		out = append(out, notePieces(cur[1:], false, l.nl)...)
		return out
	default:
		// A single line unit that now holds several paragraphs writes each on
		// its own line; inserted lines thus stay lines.
		if len(cur) == 0 {
			cur = []richtext.Paragraph{{}}
		}
		out := make([]notePiece, 0, len(cur))
		for i, p := range cur {
			t := term
			if i > 0 || t == "" {
				t = l.nl
			}
			out = append(out, notePiece{content: noteSerializePara(p, false), term: t})
		}
		return out
	}
}

func noteSerializeTableRow(p richtext.Paragraph, raw string) string {
	var cells []string
	for _, r := range p.Runs {
		parts := strings.Split(r.Text, "\t")
		for i, part := range parts {
			if i == 0 && len(cells) > 0 {
				cells[len(cells)-1] += part
			} else {
				if i > 0 || len(cells) == 0 {
					cells = append(cells, part)
				}
			}
		}
	}
	if len(cells) == 0 {
		cells = []string{""}
	}
	first := raw
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	trimmed := strings.TrimSpace(first)
	lead := strings.HasPrefix(trimmed, "|")
	trail := strings.HasSuffix(trimmed, "|")
	body := strings.Join(cells, " | ")
	if lead {
		body = "| " + body
	}
	if trail {
		body += " |"
	}
	return body
}

// noteParaKey identifies a paragraph for the untouched-line match: its block
// style and every run's text and attributes (an image by URL, not bitmap).
func noteParaKey(p richtext.Paragraph) string {
	var b strings.Builder
	st := p.Style
	b.WriteString("S")
	b.WriteByte(byte('0' + min(st.Heading, 9)))
	b.WriteByte(byte('0' + min(int(st.List), 9)))
	b.WriteByte(byte('0' + min(st.Indent, 9)))
	if st.Checked {
		b.WriteByte('c')
	}
	if st.Quote {
		b.WriteByte('q')
	}
	b.WriteByte(byte('0' + min(st.QuoteDepth, 9)))
	if st.CodeBlock {
		b.WriteByte('b')
	}
	for _, r := range p.Runs {
		a := r.Attributes
		b.WriteString("\x00")
		b.WriteString(r.Text)
		b.WriteString("|")
		b.WriteString(a.Font)
		b.WriteString("|")
		b.WriteString(strconv.FormatFloat(float64(a.Size), 'f', -1, 32))
		b.WriteString("|")
		b.WriteString(itoaSmall(a.Weight))
		if a.Italic {
			b.WriteByte('i')
		}
		if a.Underline {
			b.WriteByte('u')
		}
		if a.Strikethrough {
			b.WriteByte('s')
		}
		if a.Code {
			b.WriteByte('C')
		}
		b.WriteString("|")
		b.WriteString(a.Link)
		b.WriteString("|")
		b.WriteString(a.Tag)
		if at := a.Attachment; at != nil {
			b.WriteString("|@")
			b.WriteString(at.URL)
			b.WriteString("|")
			b.WriteString(at.Name)
		}
	}
	return b.String()
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
