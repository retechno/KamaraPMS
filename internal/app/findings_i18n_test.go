package app_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"kamarapms/internal/inventory"
	"kamarapms/internal/pos"
)

// The findings of the night audit and of the setup checks reach the screen as a code and its params, and the web writes the sentence in the language of the person (docs/architecture/35-inventory.md 5.7). A
// code that the backend can send and that has no text in English and in Indonesian would show an English sentence from the server on an Indonesian screen: this keeps each code translated.
func TestEveryCodeOfAFindingHasATextInEnglishAndIndonesian(t *testing.T) {
	codes := append(append([]string{}, inventory.FindingCodes...), pos.FindingCodes...)
	if len(codes) < 15 {
		t.Fatalf("the codes of the findings: %v", codes)
	}
	for _, lang := range []string{"en", "id"} {
		src, err := os.ReadFile("../../web/src/i18n/locales/" + lang + ".ts")
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		start := strings.Index(text, "\n  findings: {\n")
		end := strings.Index(text, "\n  nightAudit: {\n")
		if start < 0 || end < start {
			t.Fatalf("%s: no namespace findings", lang)
		}
		block := text[start:end]
		for _, c := range codes {
			if !regexp.MustCompile(`\n\s+`+regexp.QuoteMeta(c)+`: '`).MatchString(block) && !regexp.MustCompile(`\n\s+`+regexp.QuoteMeta(c)+`: "`).MatchString(block) {
				t.Errorf("%s: the finding %s has no text in findings", lang, c)
			}
		}
	}
}
