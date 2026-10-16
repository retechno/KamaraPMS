// Package printing turns a document of the point of sale (a receipt, a kitchen ticket, a drawer pulse) into the bytes a thermal printer takes
// (ESC/POS) and into a text preview of the same lines. It is pure: no database, no clock. The server renders; the agent in the hotel only
// carries the bytes (docs/architecture/34-pos.md 10.3 and 20).
package printing

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxPayload is the largest document the server hands to an agent (64 KB).
const MaxPayload = 64 * 1024

// Block kinds.
const (
	BlockText  = "text"  // a line of text, wrapped, aligned, optionally bold or double size
	BlockPair  = "pair"  // a left text and a right text on one line (a quantity and name, an amount)
	BlockRule  = "rule"  // a line of one character across the paper
	BlockBlank = "blank" // an empty line
)

// Alignments.
const (
	AlignLeft   = "L"
	AlignCenter = "C"
	AlignRight  = "R"
)

// Block is one thing on the paper. Its fields are the whole content: a document is stored as JSON and rendered again for a reprint.
type Block struct {
	T      string `json:"t"`
	Text   string `json:"x,omitempty"`
	Left   string `json:"l,omitempty"`
	Right  string `json:"r,omitempty"`
	Align  string `json:"a,omitempty"`
	Bold   bool   `json:"b,omitempty"`
	Big    bool   `json:"g,omitempty"` // double width and height
	Rule   string `json:"c,omitempty"` // the character of a rule
	Indent int    `json:"i,omitempty"`
}

// Document is what a job prints: its blocks, and whether the drawer pulses before the blocks and whether the paper is cut after them.
type Document struct {
	Kind   string  `json:"kind"` // RECEIPT, PREBILL, TICKET, DRAWER, REPORT
	Blocks []Block `json:"blocks"`
	Pulse  bool    `json:"pulse,omitempty"`
	Cut    bool    `json:"cut,omitempty"`
}

// Profile is what the printer is: its width in columns, its code page and the pin of its drawer.
type Profile struct {
	WidthMM   int    // 58 or 80
	Codepage  string // CP437, CP850, CP858, CP1252 ...
	DrawerPin int    // 0 or 1
}

// Columns is the number of characters of a line in the normal size: 32 on 58 mm, 48 on 80 mm.
func (p Profile) Columns() int {
	if p.WidthMM == 58 {
		return 32
	}
	return 48
}

// codepages maps a name to the number of ESC t. An unknown name is CP437, the power-on default of most printers.
var codepages = map[string]byte{"CP437": 0, "CP850": 2, "CP860": 3, "CP863": 4, "CP865": 5, "CP858": 19, "CP866": 17, "CP1252": 16}

// Codepages is the list of names the setup offers.
func Codepages() []string {
	return []string{"CP437", "CP850", "CP858", "CP1252", "CP860", "CP863", "CP865", "CP866"}
}

// row is one printed line, after wrapping.
type row struct {
	text  string
	align string
	bold  bool
	big   bool
}

// layout turns the blocks into printed lines for a width. mark, when it is not empty, is printed centred and bold above the document
// ("COPY 2" on a reprint): the lines under it are the same as the original's.
func layout(doc Document, p Profile, mark string) []row {
	cols := p.Columns()
	var rows []row
	if mark != "" {
		rows = append(rows, row{text: ascii(mark), align: AlignCenter, bold: true})
	}
	for _, b := range doc.Blocks {
		width := cols
		if b.Big {
			width = cols / 2
		}
		switch b.T {
		case BlockBlank:
			rows = append(rows, row{})
		case BlockRule:
			ch := ascii(b.Rule)
			if ch == "" {
				ch = "-"
			}
			rows = append(rows, row{text: strings.Repeat(ch[:1], cols), align: AlignLeft})
		case BlockPair:
			rows = append(rows, pair(ascii(b.Left), ascii(b.Right), width, b.Indent, b.Bold, b.Big)...)
		default:
			align := b.Align
			if align == "" {
				align = AlignLeft
			}
			for _, l := range wrap(ascii(b.Text), width-b.Indent) {
				rows = append(rows, row{text: strings.Repeat(" ", b.Indent) + l, align: align, bold: b.Bold, big: b.Big})
			}
		}
	}
	return rows
}

// pair puts right at the right edge of the first line of left. A left that is too long wraps under itself.
func pair(left, right string, width, indent int, bold, big bool) []row {
	rl := utf8.RuneCountInString(right)
	avail := width - indent
	if rl > 0 {
		avail -= rl + 1
	}
	if avail < 8 {
		avail = max(width-indent, 1)
		rl = 0 // the amount does not fit beside the text: it goes on a line of its own
	}
	lines := wrap(left, avail)
	if len(lines) == 0 {
		lines = []string{""}
	}
	pad := strings.Repeat(" ", indent)
	var rows []row
	for i, l := range lines {
		t := pad + l
		if i == 0 && right != "" && rl > 0 {
			fill := max(width-utf8.RuneCountInString(t)-rl, 1)
			t += strings.Repeat(" ", fill) + right
		}
		rows = append(rows, row{text: t, align: AlignLeft, bold: bold, big: big})
	}
	if right != "" && rl == 0 {
		rows = append(rows, row{text: strings.Repeat(" ", max(width-utf8.RuneCountInString(right), 0)) + right, align: AlignLeft, bold: bold, big: big})
	}
	return rows
}

// wrap breaks text into lines of at most width runes, at spaces; a word longer than a line is cut.
func wrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		flush := func() {
			if line != "" {
				out = append(out, line)
				line = ""
			}
		}
		for _, w := range words {
			for utf8.RuneCountInString(w) > width {
				r := []rune(w)
				flush()
				out = append(out, string(r[:width]))
				w = string(r[width:])
			}
			switch {
			case line == "":
				line = w
			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= width:
				line += " " + w
			default:
				flush()
				line = w
			}
		}
		flush()
	}
	return out
}

// Render makes the ESC/POS bytes of a document for a printer. The same document, profile and mark always give the same bytes.
func Render(doc Document, p Profile, mark string) ([]byte, error) {
	var b bytes.Buffer
	b.Write([]byte{0x1B, 0x40}) // ESC @ initialise
	cp, ok := codepages[p.Codepage]
	if !ok {
		cp = 0
	}
	b.Write([]byte{0x1B, 0x74, cp}) // ESC t n code page
	if doc.Pulse {
		b.Write(pulse(p))
	}
	align, bold, big := "", false, false
	for _, r := range layout(doc, p, mark) {
		if r.align != align && r.text != "" {
			n := byte(0)
			switch r.align {
			case AlignCenter:
				n = 1
			case AlignRight:
				n = 2
			}
			b.Write([]byte{0x1B, 0x61, n}) // ESC a n
			align = r.align
		}
		if r.bold != bold {
			b.Write([]byte{0x1B, 0x45, boolByte(r.bold)}) // ESC E n
			bold = r.bold
		}
		if r.big != big {
			size := byte(0x00)
			if r.big {
				size = 0x11
			}
			b.Write([]byte{0x1D, 0x21, size}) // GS ! n double width and height
			big = r.big
		}
		b.WriteString(r.text)
		b.WriteByte('\n')
	}
	if bold {
		b.Write([]byte{0x1B, 0x45, 0})
	}
	if big {
		b.Write([]byte{0x1D, 0x21, 0})
	}
	if doc.Cut {
		b.WriteString("\n\n\n")
		b.Write([]byte{0x1D, 0x56, 0x42, 0x00}) // GS V B 0 feed and partial cut
	}
	if b.Len() > MaxPayload {
		return nil, fmt.Errorf("the document is %d bytes: the limit is %d", b.Len(), MaxPayload)
	}
	return b.Bytes(), nil
}

// pulse is the drawer kick: ESC p pin on-time off-time.
func pulse(p Profile) []byte {
	return []byte{0x1B, 0x70, byte(p.DrawerPin & 1), 0x19, 0xFA}
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

// Preview is the text of the printed lines at the width of the printer, as the paper would read. A double size line is drawn with a space
// between its letters (twice as wide); bold is not shown. A drawer pulse and a cut are named in brackets.
func Preview(doc Document, p Profile, mark string) string {
	cols := p.Columns()
	var sb strings.Builder
	if doc.Pulse {
		sb.WriteString("[DRAWER PULSE]\n")
	}
	for _, r := range layout(doc, p, mark) {
		t := r.text
		if r.big {
			var w strings.Builder
			for i, c := range []rune(t) {
				if i > 0 {
					w.WriteByte(' ')
				}
				w.WriteRune(c)
			}
			t = w.String()
		}
		width := cols
		n := utf8.RuneCountInString(t)
		switch r.align {
		case AlignCenter:
			if n < width {
				t = strings.Repeat(" ", (width-n)/2) + t
			}
		case AlignRight:
			if n < width {
				t = strings.Repeat(" ", width-n) + t
			}
		}
		sb.WriteString(strings.TrimRight(t, " "))
		sb.WriteByte('\n')
	}
	if doc.Cut {
		sb.WriteString("[CUT]\n")
	}
	return sb.String()
}

// translit is what a printer without the character gets instead of it.
var translit = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O", 'Ø': "O",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'Ù': "U", 'Ú': "U", 'Û': "U", 'Ü': "U",
	'ç': "c", 'Ç': "C", 'ñ': "n", 'Ñ': "N", 'ý': "y", 'ÿ': "y", 'ß': "ss", 'æ': "ae", 'Æ': "AE", 'œ': "oe", 'Œ': "OE",
	'‘': "'", '’': "'", '“': "\"", '”': "\"", '–': "-", '—': "-", '‐': "-", '×': "x", '·': "-", '•': "*", '…': "...", '€': "EUR",
	' ': " ", '\t': " ", '\r': "",
}

// ascii makes a text printable by any printer: the characters that are not ASCII are transliterated, or become "?". A newline is kept.
func ascii(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || (r >= 0x20 && r < 0x7F):
			sb.WriteRune(r)
		default:
			if t, ok := translit[r]; ok {
				sb.WriteString(t)
			} else if r >= 0x80 {
				sb.WriteByte('?')
			}
		}
	}
	return sb.String()
}
