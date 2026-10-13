package documents

import "testing"

func TestTransferDescriptionFollowsTheLanguageOfTheDocument(t *testing.T) {
	cases := []struct {
		lang Lang
		in   string
		want string
	}{
		{LangEN, "Transfer from FOL000010", "Transfer from FOL000010"},
		{LangID, "Transfer from FOL000010", "Transfer dari FOL000010"},
		{LangID, "Transfer to FOL000019", "Transfer ke FOL000019"},
		{LangID, "Minibar 1", "Minibar 1"},
		{LangID, "Bank transfer by the guest", "Bank transfer by the guest"},
	}
	for _, c := range cases {
		if got := c.lang.TransferDescription(c.in); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.lang, c.in, got, c.want)
		}
	}
}
