package agentnotify

// Editing a tool's hook configuration without rewriting it.
//
// The files belong to the user and to the tool: key order, indentation and
// spacing are theirs. So the file is parsed into a tree that remembers byte
// spans, and every change is a splice of the original bytes. Connecting and
// then disconnecting gives the file back exactly as it was, byte for byte.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type jkind byte

const (
	jObject jkind = 'o'
	jArray  jkind = 'a'
	jString jkind = 's'
	jNumber jkind = 'n'
	jBool   jkind = 'b'
	jNull   jkind = 'z'
)

// jnode is one JSON value and the bytes it occupies.
type jnode struct {
	kind       jkind
	start, end int
	// for objects: members in file order, keys decoded.
	members []jmember
	// for arrays: items in file order.
	items []*jnode
	// for strings: the decoded text.
	text string
}

type jmember struct {
	key              string
	keyStart, keyEnd int
	value            *jnode
}

// lookup returns the member value and its index, or nil and -1.
func (n *jnode) lookup(key string) (*jnode, int) {
	if n == nil || n.kind != jObject {
		return nil, -1
	}
	for i := range n.members {
		if n.members[i].key == key {
			return n.members[i].value, i
		}
	}
	return nil, -1
}

type jparser struct {
	src []byte
	i   int
}

func (p *jparser) skipSpace() {
	for p.i < len(p.src) {
		switch p.src[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jparser) errf(format string, args ...any) error {
	return fmt.Errorf("agentnotify: settings json at byte %d: %s", p.i, fmt.Sprintf(format, args...))
}

// parseDoc parses raw. Trailing content, comments and trailing commas are
// refused: such a file is not something Rakko may rewrite.
func parseDoc(raw []byte) (*jnode, error) {
	p := &jparser{src: raw}
	p.skipSpace()
	root, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.i != len(raw) {
		return nil, p.errf("trailing content")
	}
	return root, nil
}

func (p *jparser) value(depth int) (*jnode, error) {
	if depth > 64 {
		return nil, p.errf("nested too deeply")
	}
	if p.i >= len(p.src) {
		return nil, p.errf("unexpected end")
	}
	switch c := p.src[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		start := p.i
		text, err := p.str()
		if err != nil {
			return nil, err
		}
		return &jnode{kind: jString, start: start, end: p.i, text: text}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	case bytes.HasPrefix(p.src[p.i:], []byte("true")):
		start := p.i
		p.i += 4
		return &jnode{kind: jBool, start: start, end: p.i, text: "true"}, nil
	case bytes.HasPrefix(p.src[p.i:], []byte("false")):
		start := p.i
		p.i += 5
		return &jnode{kind: jBool, start: start, end: p.i, text: "false"}, nil
	case bytes.HasPrefix(p.src[p.i:], []byte("null")):
		start := p.i
		p.i += 4
		return &jnode{kind: jNull, start: start, end: p.i}, nil
	}
	return nil, p.errf("unexpected %q", p.src[p.i])
}

// number scans one JSON number by its own grammar. A file whose numbers are
// malformed is not a file Rakko may rewrite, so "1e", "01" and "-" must be
// refused here rather than swallowed as a token.
func (p *jparser) number() (*jnode, error) {
	start := p.i
	if p.src[p.i] == '-' {
		p.i++
	}
	switch {
	case p.i < len(p.src) && p.src[p.i] == '0':
		p.i++
	case p.i < len(p.src) && p.src[p.i] >= '1' && p.src[p.i] <= '9':
		for p.i < len(p.src) && isDigit(p.src[p.i]) {
			p.i++
		}
	default:
		return nil, p.errf("bad number")
	}
	if p.i < len(p.src) && p.src[p.i] == '.' {
		p.i++
		if p.i >= len(p.src) || !isDigit(p.src[p.i]) {
			return nil, p.errf("bad number")
		}
		for p.i < len(p.src) && isDigit(p.src[p.i]) {
			p.i++
		}
	}
	if p.i < len(p.src) && (p.src[p.i] == 'e' || p.src[p.i] == 'E') {
		p.i++
		if p.i < len(p.src) && (p.src[p.i] == '+' || p.src[p.i] == '-') {
			p.i++
		}
		if p.i >= len(p.src) || !isDigit(p.src[p.i]) {
			return nil, p.errf("bad number")
		}
		for p.i < len(p.src) && isDigit(p.src[p.i]) {
			p.i++
		}
	}
	return &jnode{kind: jNumber, start: start, end: p.i}, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *jparser) str() (string, error) {
	start := p.i
	p.i++ // opening quote
	for p.i < len(p.src) {
		switch p.src[p.i] {
		case '\\':
			p.i += 2
			continue
		case '"':
			p.i++
			var out string
			if err := json.Unmarshal(p.src[start:p.i], &out); err != nil {
				return "", p.errf("bad string: %v", err)
			}
			return out, nil
		case '\n':
			return "", p.errf("newline in a string")
		}
		p.i++
	}
	return "", p.errf("unterminated string")
}

func (p *jparser) object(depth int) (*jnode, error) {
	n := &jnode{kind: jObject, start: p.i}
	p.i++
	p.skipSpace()
	if p.i < len(p.src) && p.src[p.i] == '}' {
		p.i++
		n.end = p.i
		return n, nil
	}
	for {
		p.skipSpace()
		if p.i >= len(p.src) || p.src[p.i] != '"' {
			return nil, p.errf("object key expected")
		}
		keyStart := p.i
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		keyEnd := p.i
		p.skipSpace()
		if p.i >= len(p.src) || p.src[p.i] != ':' {
			return nil, p.errf("':' expected")
		}
		p.i++
		p.skipSpace()
		val, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		n.members = append(n.members, jmember{key: key, keyStart: keyStart, keyEnd: keyEnd, value: val})
		p.skipSpace()
		if p.i >= len(p.src) {
			return nil, p.errf("unterminated object")
		}
		switch p.src[p.i] {
		case ',':
			p.i++
			p.skipSpace()
			if p.i < len(p.src) && p.src[p.i] == '}' {
				return nil, p.errf("trailing comma")
			}
		case '}':
			p.i++
			n.end = p.i
			return n, nil
		default:
			return nil, p.errf("',' or '}' expected")
		}
	}
}

func (p *jparser) array(depth int) (*jnode, error) {
	n := &jnode{kind: jArray, start: p.i}
	p.i++
	p.skipSpace()
	if p.i < len(p.src) && p.src[p.i] == ']' {
		p.i++
		n.end = p.i
		return n, nil
	}
	for {
		p.skipSpace()
		item, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		n.items = append(n.items, item)
		p.skipSpace()
		if p.i >= len(p.src) {
			return nil, p.errf("unterminated array")
		}
		switch p.src[p.i] {
		case ',':
			p.i++
			p.skipSpace()
			if p.i < len(p.src) && p.src[p.i] == ']' {
				return nil, p.errf("trailing comma")
			}
		case ']':
			p.i++
			n.end = p.i
			return n, nil
		default:
			return nil, p.errf("',' or ']' expected")
		}
	}
}

// errNotAnObject marks a file whose top level is not an object.
var errNotAnObject = errors.New("agentnotify: settings file is not a JSON object")

// jsonDoc is a settings file being edited in place.
type jsonDoc struct {
	src  []byte
	root *jnode
	// indent is the file's own indentation step, colon its key separator.
	indent string
	colon  string
}

func newJSONDoc(raw []byte) (*jsonDoc, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	root, err := parseDoc(raw)
	if err != nil {
		return nil, err
	}
	if root.kind != jObject {
		return nil, errNotAnObject
	}
	d := &jsonDoc{src: raw, root: root}
	d.indent, d.colon = detectStyle(raw)
	return d, nil
}

// detectStyle reads the file's indentation step and key separator so new
// lines look like the ones around them.
func detectStyle(src []byte) (indent, colon string) {
	indent, colon = "  ", ": "
	lines := bytes.Split(src, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimLeft(line, " ")
		if len(trimmed) == len(line) || len(trimmed) == 0 {
			continue
		}
		// The first indented line sets the step; a deeper line only counts
		// when it is a multiple of the step we already have.
		spaces := len(line) - len(trimmed)
		if indent == "  " {
			indent = strings.Repeat(" ", spaces)
		}
		break
	}
	if len(indent) > 8 {
		indent = "  "
	}
	// The colon style comes from the first key in the file.
	for i := 0; i+1 < len(src); i++ {
		if src[i] != '"' {
			continue
		}
		j := i + 1
		for j < len(src) && src[j] != '"' {
			j++
		}
		if j+1 < len(src) && src[j+1] == ':' {
			k := j + 1 // the colon itself
			colon = ""
			for k < len(src) && (src[k] == ':' || src[k] == ' ' || src[k] == '\t') {
				colon += string(src[k])
				k++
			}
			return indent, colon
		}
	}
	return indent, colon
}

// bytes returns the current document text.
func (d *jsonDoc) bytes() []byte { return d.src }

// splice replaces src[from:to] with repl and re-parses, so spans stay valid.
func (d *jsonDoc) splice(from, to int, repl string) error {
	next := make([]byte, 0, len(d.src)-(to-from)+len(repl))
	next = append(next, d.src[:from]...)
	next = append(next, repl...)
	next = append(next, d.src[to:]...)
	root, err := parseDoc(next)
	if err != nil {
		return err
	}
	if root.kind != jObject {
		return errNotAnObject
	}
	d.src, d.root = next, root
	return nil
}

// lineIndent is the leading whitespace of the line that holds pos.
func (d *jsonDoc) lineIndent(pos int) string {
	start := bytes.LastIndexByte(d.src[:pos], '\n') + 1
	end := start
	for end < len(d.src) && (d.src[end] == ' ' || d.src[end] == '\t') {
		end++
	}
	return string(d.src[start:end])
}

// closeAndTail finds the closing bracket of a container and the start of the
// whitespace run before it. The run never includes the opening bracket: an
// empty "{}" or "[]" has no whitespace to replace, so insertion happens just
// after the bracket.
func (d *jsonDoc) closeAndTail(n *jnode) (close, tailStart int) {
	close = n.end - 1
	tailStart = close
	for tailStart > n.start+1 && isJSONSpace(d.src[tailStart-1]) {
		tailStart--
	}
	if tailStart < n.start+1 {
		tailStart = n.start + 1
	}
	return close, tailStart
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// appendItem adds one element to arr. render receives the indentation the
// element's own first line should not include (the caller writes inner lines
// with it) and returns the element text.
func (d *jsonDoc) appendItem(arr *jnode, render func(indent string) string) error {
	close, tailStart := d.closeAndTail(arr)
	if len(arr.items) == 0 {
		parentIndent := d.lineIndent(arr.start)
		indent := parentIndent + d.indent
		return d.splice(tailStart, close, "\n"+indent+render(indent)+"\n"+parentIndent)
	}
	indent := d.lineIndent(arr.items[0].start)
	if indent == "" {
		indent = d.lineIndent(arr.start) + d.indent
	}
	return d.splice(tailStart, tailStart, ",\n"+indent+render(indent))
}

// removeItem drops one element of arr, taking its separator with it.
func (d *jsonDoc) removeItem(arr *jnode, idx int) error {
	switch {
	case len(arr.items) == 1:
		return d.splice(arr.start, arr.end, "[]")
	case idx > 0:
		return d.splice(arr.items[idx-1].end, arr.items[idx].end, "")
	default:
		return d.splice(arr.items[0].start, arr.items[1].start, "")
	}
}

// appendMember adds key to obj. render receives the key line's indentation
// and returns the value text.
func (d *jsonDoc) appendMember(obj *jnode, key string, render func(indent string) string) error {
	close, tailStart := d.closeAndTail(obj)
	quoted := quoteJSON(key)
	if len(obj.members) == 0 {
		parentIndent := d.lineIndent(obj.start)
		indent := parentIndent + d.indent
		return d.splice(tailStart, close, "\n"+indent+quoted+d.colon+render(indent)+"\n"+parentIndent)
	}
	indent := d.lineIndent(obj.members[0].keyStart)
	if indent == "" {
		indent = d.lineIndent(obj.start) + d.indent
	}
	return d.splice(tailStart, tailStart, ",\n"+indent+quoted+d.colon+render(indent))
}

// removeMember drops obj's key, taking its separator with it.
func (d *jsonDoc) removeMember(obj *jnode, idx int) error {
	switch {
	case len(obj.members) == 1:
		return d.splice(obj.start, obj.end, "{}")
	case idx > 0:
		return d.splice(obj.members[idx-1].value.end, obj.members[idx].value.end, "")
	default:
		return d.splice(obj.members[0].keyStart, obj.members[1].keyStart, "")
	}
}

// quoteJSON renders a key or value as a JSON string.
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
