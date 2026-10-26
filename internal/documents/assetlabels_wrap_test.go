package documents

import (
	"testing"

	"github.com/go-pdf/fpdf"

	"kamarapms/internal/assets"
)

func TestWrapKeepsAtMostTwoLinesAndEndsWithAnEllipsis(t *testing.T) {
	p := fpdf.New("P", "mm", "A4", "")
	p.AddPage()
	p.SetFont("Helvetica", "", 8)
	const w = 33.0
	cases := map[string]int{
		"Sofa": 1,
		"Wooden wardrobe two doors with a mirror and drawers":           2,
		"Supercalifragilisticexpialidocious-and-more-and-more-and-more": 2,
	}
	for in, want := range cases {
		got := wrap(p, in, w, 2)
		if len(got) != want {
			t.Errorf("%q: %d lines, want %d: %q", in, len(got), want, got)
		}
		for _, l := range got {
			if p.GetStringWidth(l) > w {
				t.Errorf("%q: the line %q is wider than %v mm", in, l, w)
			}
		}
	}
	long := wrap(p, "Wooden wardrobe two doors with a mirror and drawers", w, 2)
	if l := long[1]; len(l) < 4 || l[len(l)-3:] != "..." {
		t.Errorf("a name that does not fit must end in an ellipsis: %q", long)
	}
	if short := wrap(p, "Sofa lobi", w, 2); len(short) != 1 || short[0] != "Sofa lobi" {
		t.Errorf("a short name must stay as it is: %q", short)
	}
}

func TestLabelsWithALongNameFitTheSheet(t *testing.T) {
	long := "Lemari kayu jati dua pintu dengan cermin besar dan laci tersembunyi di bagian bawah"
	labels := []assets.Label{{Code: "FA:ABCDEFGH2345", Title: "FA-000001", Name: long, Subtitle: "Kamar 101"}, {Code: "FA:ABCDEFGH2346", Title: "FA-000002", Name: "Sofa", Subtitle: "Lobi"}}
	for _, layout := range []string{LabelLayoutA4, LabelLayoutRoll} {
		b, err := RenderAssetLabels(labels, layout)
		if err != nil || len(b) < 500 {
			t.Fatalf("%s: %v (%d bytes)", layout, err, len(b))
		}
	}
}
