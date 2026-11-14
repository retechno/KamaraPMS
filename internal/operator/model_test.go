package operator

import (
	"regexp"
	"strings"
	"testing"
)

func TestRecoveryCodeShape(t *testing.T) {
	shape := regexp.MustCompile(`^[ABCDEFGHJKMNPQRSTVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTVWXYZ23456789]{2}$`)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		c, err := NewRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(c) {
			t.Fatalf("code %q is not XXXX-XXXX-XX from the alphabet", c)
		}
		if seen[c] {
			t.Fatalf("code %q drawn twice in 500", c)
		}
		seen[c] = true
	}
}

func TestRecoveryCodeNormalisation(t *testing.T) {
	for _, in := range []string{"abcd-efgh-jk", "ABCD EFGH JK", "  abcdefghjk ", "ABCD-EFGH-JK"} {
		got, ok := NormaliseRecoveryCode(in)
		if !ok || got != "ABCDEFGHJK" {
			t.Errorf("NormaliseRecoveryCode(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "ABCD-EFGH-J", "ABCD-EFGH-JKL", "ABCD-EFGH-J0", "ABCD-EFGH-JI", "ABCD-EFGH-J!"} {
		if got, ok := NormaliseRecoveryCode(in); ok {
			t.Errorf("NormaliseRecoveryCode(%q) = %q, true; it cannot be a recovery code", in, got)
		}
	}
}

func TestRecoveryCodeHashIsOfTheNormalisedForm(t *testing.T) {
	a, _ := NormaliseRecoveryCode("abcd-efgh-jk")
	b, _ := NormaliseRecoveryCode("ABCDEFGHJK")
	if string(HashRecoveryCode(a)) != string(HashRecoveryCode(b)) {
		t.Fatal("the same code in two writings has two hashes")
	}
	if len(HashRecoveryCode(a)) != 32 {
		t.Fatal("a SHA-256 is 32 bytes")
	}
}

func TestNormaliseEmail(t *testing.T) {
	if got := NormaliseEmail("  Op@Example.TEST "); got != "op@example.test" || strings.ContainsAny(got, " A") {
		t.Fatalf("got %q", got)
	}
}
