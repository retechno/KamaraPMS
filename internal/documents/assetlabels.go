package documents

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	qrcode "github.com/skip2/go-qrcode"

	"kamarapms/internal/assets"
	"kamarapms/internal/platform/apperr"
)

// The QR labels of the assets and of the places (docs/architecture/37-fixed-assets.md 4.7, Q12). The QR holds the code only ("FA:<code>" for an asset, "FL:<code>" for a place): never a value, a
// name or an address, so a label that falls into other hands tells nothing. The encoder is github.com/skip2/go-qrcode, pure Go, pinned in go.mod.

// Layouts of the sheet.
const (
	LabelLayoutA4    = "A4_24"      // an A4 sheet of 3 x 8 labels of 66 x 35 mm, inside a margin of 6 mm at the sides and 8.5 mm above and below
	LabelLayoutRoll  = "ROLL_50x30" // a roll of labels of 50 x 30 mm, one to a page
	labelsPerA4Sheet = 24
)

// SetAssets gives the documents the register whose labels they print (the register does not know the documents, so the application ties the two).
func (s *Service) SetAssets(a *assets.Service) { s.assets = a }

// QRMatrix encodes content as a QR code (error correction M) and answers its modules, true for a dark one, with the quiet zone included.
func QRMatrix(content string) ([][]bool, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	return q.Bitmap(), nil
}

var placeKind = map[Lang]map[string]string{
	LangEN: {"ROOM": "Room", "BANQUET_ROOM": "Meeting room", "WAREHOUSE": "Warehouse", "AREA": "Area"},
	LangID: {"ROOM": "Kamar", "BANQUET_ROOM": "Ruang pertemuan", "WAREHOUSE": "Gudang", "AREA": "Area"},
}

// AssetLabels renders the labels of assets (or of places) as a PDF (any asset permission).
func (s *Service) AssetLabels(ctx context.Context, propertyID int64, f assets.LabelFilter, layout string) (Document, error) {
	if s.assets == nil {
		return Document{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the fixed assets are not available")
	}
	if layout == "" {
		layout = LabelLayoutA4
	}
	if layout != LabelLayoutA4 && layout != LabelLayoutRoll {
		return Document{}, apperr.Invalid("the layout is invalid", apperr.FieldError{Field: "layout", Code: "INVALID_VALUE", Message: "A4_24 or ROLL_50x30"})
	}
	labels, err := s.assets.Labels(ctx, propertyID, f)
	if err != nil {
		return Document{}, err
	}
	lang := LangFrom(ctx)
	if f.Places {
		for i := range labels {
			if t, ok := placeKind[lang][labels[i].Subtitle]; ok {
				labels[i].Subtitle = t
			}
		}
	}
	b, err := RenderAssetLabels(labels, layout)
	name := "asset-labels.pdf"
	if f.Places {
		name = "place-labels.pdf"
	}
	return Document{Filename: name, PDF: b}, err
}

// RenderAssetLabels draws the labels on a sheet of the layout. The bytes depend on the labels only.
func RenderAssetLabels(labels []assets.Label, layout string) ([]byte, error) {
	var p *fpdf.Fpdf
	var w, h float64
	var cols, perPage int
	var ox, oy float64 // where the first label starts: the margin of the paper
	if layout == LabelLayoutRoll {
		w, h, cols, perPage = 50, 30, 1, 1
		p = fpdf.NewCustom(&fpdf.InitType{OrientationStr: "L", UnitStr: "mm", Size: fpdf.SizeType{Wd: w, Ht: h}})
	} else {
		w, h, cols, perPage = 66, 35, 3, labelsPerA4Sheet
		ox, oy = 6, 8.5
		p = fpdf.New("P", "mm", "A4", "")
	}
	p.SetMargins(0, 0, 0)
	p.SetAutoPageBreak(false, 0)
	p.SetCatalogSort(true)
	p.SetCompression(compressPDF)
	fixed := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	p.SetCreationDate(fixed)
	p.SetModificationDate(fixed)
	p.SetCreator("KamaraPMS", true)
	tr := p.UnicodeTranslatorFromDescriptor("")
	text := func(s string) string { return latin1(tr(s)) }
	for i, l := range labels {
		slot := i % perPage
		if slot == 0 {
			p.AddPage()
		}
		x, y := ox+float64(slot%cols)*w, oy+float64(slot/cols)*h
		m, err := QRMatrix(l.Code)
		if err != nil {
			return nil, err
		}
		qr := h - 6
		if qr > 28 {
			qr = 28
		}
		cell := qr / float64(len(m))
		qx, qy := x+2, y+(h-qr)/2
		p.SetFillColor(0, 0, 0)
		for r, row := range m {
			for c, dark := range row {
				if dark {
					p.Rect(qx+float64(c)*cell, qy+float64(r)*cell, cell, cell, "F")
				}
			}
		}
		tx, tw := qx+qr+2, w-qr-6
		const nameLines, nameLH = 2, 3.6
		top := y + (h-(5+nameLines*nameLH+4+3))/2
		p.SetTextColor(0, 0, 0)
		p.SetFont("Helvetica", "B", 10)
		p.SetXY(tx, top)
		p.CellFormat(tw, 5, clip(p, text(l.Title), tw), "", 0, "L", false, 0, "")
		p.SetFont("Helvetica", "", 8)
		for n, line := range wrap(p, text(l.Name), tw, nameLines) {
			p.SetXY(tx, top+5+float64(n)*nameLH)
			p.CellFormat(tw, nameLH, line, "", 0, "L", false, 0, "")
		}
		p.SetXY(tx, top+5+nameLines*nameLH)
		p.CellFormat(tw, 4, clip(p, text(l.Subtitle), tw), "", 0, "L", false, 0, "")
		p.SetFont("Helvetica", "", 6)
		p.SetTextColor(110, 110, 110)
		p.SetXY(tx, top+9+nameLines*nameLH)
		p.CellFormat(tw, 3, strings.TrimPrefix(strings.TrimPrefix(l.Code, "FA:"), "FL:"), "", 0, "L", false, 0, "")
	}
	var buf bytes.Buffer
	if err := p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// clip cuts s to fit w mm in the current font.
func clip(p *fpdf.Fpdf, s string, w float64) string {
	for len(s) > 1 && p.GetStringWidth(s) > w {
		s = s[:len(s)-1]
	}
	return s
}

// wrap breaks s into at most n lines of w mm in the current font, at the spaces (a word wider than a line is cut). What does not fit ends the last line with "...".
func wrap(p *fpdf.Fpdf, s string, w float64, n int) []string {
	var pieces []string
	for _, word := range strings.Fields(s) {
		for len(word) > 1 && p.GetStringWidth(word) > w {
			cut := clip(p, word, w)
			pieces = append(pieces, cut)
			word = word[len(cut):]
		}
		pieces = append(pieces, word)
	}
	var lines []string
	cur := ""
	for _, piece := range pieces {
		if cand := strings.TrimSpace(cur + " " + piece); cur == "" || p.GetStringWidth(cand) <= w {
			cur = cand
			continue
		}
		lines = append(lines, cur)
		cur = piece
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) <= n {
		return lines
	}
	last := lines[n-1]
	for len(last) > 0 && p.GetStringWidth(last+"...") > w {
		last = last[:len(last)-1]
	}
	lines = lines[:n]
	lines[n-1] = strings.TrimRight(last, " ") + "..."
	return lines
}
