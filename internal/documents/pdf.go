// Package documents renders the printed documents of the hotel as PDF: the guest invoice (or bill), the registration
// card, the payment receipt and the reservation confirmation. The rendering takes plain data and is deterministic
// (no clock, no randomness), so it is tested without a database; the service assembles the data through the
// owning services, which enforce the permissions.
package documents

import (
	"bytes"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// Hotel is the letterhead.
type Hotel struct {
	Name, Address, City, Country, Phone, Email, TaxID, Footer string
}

func (h Hotel) lines() []string {
	var out []string
	if h.Address != "" {
		out = append(out, h.Address)
	}
	if loc := strings.TrimSpace(strings.Join(nonEmpty(h.City, h.Country), ", ")); loc != "" {
		out = append(out, loc)
	}
	if c := strings.Join(nonEmpty(phoneLabel(h.Phone), emailLabel(h.Email)), "  |  "); c != "" {
		out = append(out, c)
	}
	if h.TaxID != "" {
		out = append(out, "Tax ID: "+h.TaxID)
	}
	return out
}

func phoneLabel(s string) string {
	if s == "" {
		return ""
	}
	return "Tel " + s
}

func emailLabel(s string) string { return s }

func nonEmpty(in ...string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// col is a table column: its width in mm, header and alignment ("L", "R" or "C").
type col struct {
	w     float64
	head  string
	align string
}

const (
	pageW    = 210.0
	margin   = 15.0
	bodyW    = pageW - 2*margin
	lineH    = 5.0
	footerAt = 282.0
)

var (
	ink    = [3]int{33, 37, 41}
	muted  = [3]int{108, 117, 125}
	band   = [3]int{238, 241, 245}
	accent = [3]int{31, 64, 104}
	alert  = [3]int{176, 0, 32}
)

// compressPDF is turned off by the tests, which look for text in the page streams.
var compressPDF = true

// page is a PDF under construction.
type page struct {
	p       *fpdf.Fpdf
	tr      func(string) string
	hotel   Hotel
	printed string
	lang    Lang
}

// newPage starts an A4 document with the hotel letterhead. printed is the "printed on" text of the footer.
func newPage(h Hotel, lang Lang, title, printed string) *page {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetMargins(margin, margin, margin)
	p.SetAutoPageBreak(false, 0)
	p.SetCatalogSort(true)
	p.SetCompression(compressPDF)
	p.SetCreationDate(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) // the bytes depend on the content only
	p.SetTitle(lang.T(title), true)
	p.SetCreator("KamaraPMS", true)
	p.AliasNbPages("")
	translate := p.UnicodeTranslatorFromDescriptor("")
	g := &page{p: p, tr: func(s string) string { return latin1(translate(s)) }, hotel: h, printed: printed, lang: lang}
	p.SetFooterFunc(g.footer)
	p.AddPage()
	g.letterhead()
	return g
}

// latin1 cleans what UnicodeTranslator returns. Its output is raw cp1252 bytes (not UTF-8; what the core fonts
// cannot show became '.'), so it is handled byte by byte. Control characters other than the newline become spaces.
func latin1(s string) string {
	b := []byte(s)
	for i, c := range b {
		if (c < 32 && c != '\n') || c == 127 {
			b[i] = ' '
		}
	}
	return string(b)
}

// split wraps text to a width in mm. fpdf's own SplitText and MultiCell read the translated bytes as UTF-8 and
// panic on accented letters, so wrapping is done here with the byte-based width.
func (g *page) split(s string, w float64) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		cur := ""
		for _, word := range strings.Fields(para) {
			for g.p.GetStringWidth(word) > w && len(word) > 1 {
				n := len(word) - 1
				for n > 1 && g.p.GetStringWidth(word[:n]) > w {
					n--
				}
				if cur != "" {
					lines = append(lines, cur)
					cur = ""
				}
				lines = append(lines, word[:n])
				word = word[n:]
			}
			try := word
			if cur != "" {
				try = cur + " " + word
			}
			if g.p.GetStringWidth(try) <= w {
				cur = try
			} else {
				lines = append(lines, cur)
				cur = word
			}
		}
		lines = append(lines, cur)
	}
	return lines
}

// paragraph prints wrapped text at the left margin.
func (g *page) paragraph(s string, h float64) {
	for _, l := range g.split(g.tr(s), bodyW-2) {
		g.p.CellFormat(bodyW, h, l, "", 1, "L", false, 0, "")
	}
}

func (g *page) color(c [3]int) { g.p.SetTextColor(c[0], c[1], c[2]) }

func (g *page) font(style string, size float64) { g.p.SetFont("Helvetica", style, size) }

func (g *page) letterhead() {
	g.color(accent)
	g.font("B", 16)
	g.p.CellFormat(bodyW, 8, g.tr(g.hotel.Name), "", 1, "L", false, 0, "")
	g.color(muted)
	g.font("", 9)
	for _, l := range g.hotel.lines() {
		g.p.CellFormat(bodyW, 4.2, g.tr(g.lang.T(l)), "", 1, "L", false, 0, "")
	}
	g.p.Ln(2)
	g.p.SetDrawColor(accent[0], accent[1], accent[2])
	g.p.SetLineWidth(0.5)
	g.p.Line(margin, g.p.GetY(), pageW-margin, g.p.GetY())
	g.p.Ln(4)
	g.color(ink)
}

func (g *page) footer() {
	g.p.SetY(-18)
	g.p.SetDrawColor(band[0]-20, band[1]-20, band[2]-20)
	g.p.SetLineWidth(0.2)
	g.p.Line(margin, g.p.GetY(), pageW-margin, g.p.GetY())
	g.p.Ln(1.5)
	g.color(muted)
	g.font("I", 8)
	if g.hotel.Footer != "" {
		for _, l := range g.split(g.tr(g.hotel.Footer), bodyW-2) {
			g.p.CellFormat(bodyW, 3.8, l, "", 1, "C", false, 0, "")
		}
	}
	g.font("", 7.5)
	g.p.SetY(-9)
	g.p.CellFormat(bodyW/2, 4, g.tr(g.printed), "", 0, "L", false, 0, "")
	g.p.CellFormat(bodyW/2, 4, g.tr(g.lang.T("Page "+itoa(g.p.PageNo())+" of {nb}")), "", 0, "R", false, 0, "")
	g.color(ink)
}

// title prints the document title with its number on the right.
func (g *page) title(title, number string) {
	g.font("B", 14)
	g.color(ink)
	g.p.CellFormat(bodyW*0.6, 8, g.tr(g.lang.T(title)), "", 0, "L", false, 0, "")
	g.font("B", 11)
	g.p.CellFormat(bodyW*0.4, 8, g.tr(number), "", 1, "R", false, 0, "")
	g.p.Ln(1)
}

// pairs prints label/value pairs in two columns.
func (g *page) pairs(items [][2]string) {
	half := bodyW / 2
	for i := 0; i < len(items); i += 2 {
		y := g.p.GetY()
		h := 0.0
		for j := 0; j < 2 && i+j < len(items); j++ {
			g.p.SetXY(margin+float64(j)*half, y)
			g.font("", 8)
			g.color(muted)
			g.p.CellFormat(half-2, 3.8, g.tr(g.lang.T(items[i+j][0])), "", 2, "L", false, 0, "")
			g.font("B", 10)
			g.color(ink)
			g.p.SetX(margin + float64(j)*half)
			lines := g.split(g.tr(blank(g.lang.T(items[i+j][1]))), half-4)
			for _, l := range lines {
				g.p.CellFormat(half-2, 4.6, l, "", 2, "L", false, 0, "")
			}
			if used := 3.8 + 4.6*float64(len(lines)); used > h {
				h = used
			}
		}
		g.p.SetXY(margin, y+h+1.5)
	}
}

func blank(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// section prints a small heading.
func (g *page) section(s string) {
	g.p.Ln(2)
	g.font("B", 10)
	g.color(accent)
	g.p.CellFormat(bodyW, 6, g.tr(strings.ToUpper(g.lang.T(s))), "", 1, "L", false, 0, "")
	g.color(ink)
}

// table prints rows under a header, wrapping long cells and repeating the header on a new page. The columns are
// stretched to the body width when they are narrower in total.
func (g *page) table(cols []col, rows [][]string) { g.tableB(cols, rows, nil) }

// tableB is table with some rows in bold (totals and subtotals of a statement).
func (g *page) tableB(cols []col, rows [][]string, bold func(row int) bool) {
	head := func() {
		g.p.SetFillColor(band[0], band[1], band[2])
		g.font("B", 8.5)
		for i, c := range cols {
			g.p.CellFormat(c.w, 6.5, g.tr(g.lang.T(c.head)), "", boolInt(i == len(cols)-1), c.align, true, 0, "")
		}
	}
	head()
	g.font("", 9)
	for ri, r := range rows {
		lines := 1
		wrapped := make([][]string, len(cols))
		for i, c := range cols {
			s := ""
			if i < len(r) {
				s = r[i]
			}
			wrapped[i] = g.split(g.tr(g.lang.Word(s)), c.w-2)
			if len(wrapped[i]) == 0 {
				wrapped[i] = []string{""}
			}
			if len(wrapped[i]) > lines {
				lines = len(wrapped[i])
			}
		}
		h := float64(lines)*4.4 + 1.6
		if g.p.GetY()+h > footerAt-14 {
			g.p.AddPage()
			head()
			g.font("", 9)
		}
		y := g.p.GetY()
		if bold != nil && bold(ri) {
			g.font("B", 9)
		}
		for i, c := range cols {
			x := margin
			for _, p := range cols[:i] {
				x += p.w
			}
			g.p.SetXY(x, y+0.8)
			for _, l := range wrapped[i] {
				g.p.CellFormat(c.w-1, 4.4, l, "", 2, c.align, false, 0, "")
			}
		}
		g.font("", 9)
		g.p.SetDrawColor(220, 224, 229)
		g.p.SetLineWidth(0.15)
		g.p.Line(margin, y+h, margin+sum(cols), y+h)
		g.p.SetXY(margin, y+h)
	}
}

func sum(cols []col) float64 {
	t := 0.0
	for _, c := range cols {
		t += c.w
	}
	return t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// totals prints right-aligned label/amount lines; the last one is emphasised.
func (g *page) totals(items [][2]string) {
	g.p.Ln(2)
	for i, it := range items {
		last := i == len(items)-1
		if last {
			g.font("B", 11)
		} else {
			g.font("", 9.5)
		}
		if g.p.GetY() > footerAt-24 {
			g.p.AddPage()
		}
		g.p.SetX(margin + bodyW*0.5)
		g.p.CellFormat(bodyW*0.3, 5.6, g.tr(g.lang.T(it[0])), "", 0, "L", false, 0, "")
		g.p.CellFormat(bodyW*0.2, 5.6, g.tr(it[1]), "", 1, "R", false, 0, "")
	}
}

// note prints a wrapped paragraph.
func (g *page) note(s string, style string, size float64) {
	g.font(style, size)
	g.paragraph(g.lang.T(s), 4.4)
}

// signature prints a signature line with a caption.
func (g *page) signature(x, y, w float64, caption string) {
	g.p.SetDrawColor(ink[0], ink[1], ink[2])
	g.p.SetLineWidth(0.2)
	g.p.Line(x, y, x+w, y)
	g.p.SetXY(x, y+1)
	g.font("", 8)
	g.color(muted)
	g.p.CellFormat(w, 4, g.tr(g.lang.T(caption)), "", 0, "L", false, 0, "")
	g.color(ink)
}

func (g *page) stamp(s string) {
	g.font("B", 18)
	g.color(alert)
	g.p.CellFormat(bodyW, 10, g.tr(g.lang.T(s)), "", 1, "C", false, 0, "")
	g.color(ink)
}

func (g *page) bytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := g.p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

var monthShort = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
