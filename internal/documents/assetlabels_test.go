package documents_test

import (
	"bytes"
	"testing"

	"kamarapms/internal/assets"
	"kamarapms/internal/documents"
)

func TestQRMatrixHasTheFinderPatterns(t *testing.T) {
	m, err := documents.QRMatrix("FA:K7M2QX4P")
	if err != nil {
		t.Fatal(err)
	}
	n := len(m)
	if n < 25 { // version 2 or more, with the quiet zone
		t.Fatalf("%d modules", n)
	}
	// the quiet zone is light, the finder patterns are dark rings around a dark centre at three corners
	if m[0][0] || m[n-1][n-1] {
		t.Error("the quiet zone is not light")
	}
	z := 4
	for _, corner := range [][2]int{{z, z}, {z, n - z - 7}, {n - z - 7, z}} {
		r, c := corner[0], corner[1]
		if !m[r][c] || !m[r+6][c+6] || !m[r+3][c+3] || m[r+1][c+1] || m[r+5][c+5] {
			t.Errorf("no finder pattern at %v", corner)
		}
	}
	other, _ := documents.QRMatrix("FA:ZZZZZZZZ")
	same := true
	for r := range m {
		for c := range m[r] {
			same = same && m[r][c] == other[r][c]
		}
	}
	if same {
		t.Error("two codes give the same picture")
	}
}

func TestLabelsRenderInBothLayoutsAndDoNotChange(t *testing.T) {
	var labels []assets.Label
	for i := 0; i < 30; i++ {
		labels = append(labels, assets.Label{Code: "FA:ABCD" + string(rune('A'+i%26)) + "234", Title: "FA-000001", Name: "Sofa lobi", Subtitle: "Lobby"})
	}
	a4, err := documents.RenderAssetLabels(labels, documents.LabelLayoutA4)
	if err != nil {
		t.Fatal(err)
	}
	roll, err := documents.RenderAssetLabels(labels[:3], documents.LabelLayoutRoll)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"a4": a4, "roll": roll} {
		if !bytes.HasPrefix(b, []byte("%PDF-")) {
			t.Errorf("%s is not a PDF", name)
		}
	}
	if n := bytes.Count(a4, []byte("/Type /Page\n")); n != 2 {
		t.Errorf("30 labels on 24 to a sheet are %d pages, want 2", n)
	}
	if n := bytes.Count(roll, []byte("/Type /Page\n")); n != 3 {
		t.Errorf("3 labels on a roll are %d pages, want 3", n)
	}
	again, _ := documents.RenderAssetLabels(labels, documents.LabelLayoutA4)
	if !bytes.Equal(a4, again) {
		t.Error("the same labels gave other bytes")
	}
}
