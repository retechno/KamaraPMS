package csvlang_test

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"kamarapms/internal/platform/csvlang"
)

func TestHeader(t *testing.T) {
	header := []string{"business_date", "rooms_house_use", "something_new"}
	en := httptest.NewRequest("GET", "/x?format=csv", nil)
	if !reflect.DeepEqual(csvlang.Header(en, header), header) {
		t.Fatal("English keeps the stable names")
	}
	id := httptest.NewRequest("GET", "/x?format=csv&lang=id", nil)
	got := csvlang.Header(id, header)
	if !reflect.DeepEqual(got, []string{"Tanggal bisnis", "Kamar house use", "something_new"}) {
		t.Fatalf("Indonesian: %v", got)
	}
	if header[0] != "business_date" {
		t.Fatal("the header passed in is not changed")
	}
	if csvlang.Word(id, "Total") != "Total" || csvlang.Word(id, "Opening balance") != "Saldo awal" {
		t.Fatal("fixed cells")
	}
}
