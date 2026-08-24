package customers

import (
	"regexp"
	"testing"
)

func TestGenerateCode(t *testing.T) {
	codeRe := regexp.MustCompile(`^[0-9]{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		if !codeRe.MatchString(code) {
			t.Fatalf("code %q is not 6 digits", code)
		}
		seen[code] = true
	}
	// 100 draws over a 10^6 space: collisions possible but 100 distinct is
	// overwhelmingly expected — this only catches a degenerate generator.
	if len(seen) < 95 {
		t.Errorf("codes look non-random: only %d distinct in 100 draws", len(seen))
	}
}

func TestHashCode(t *testing.T) {
	a, b := HashCode("123456"), HashCode("123456")
	if a != b {
		t.Fatal("hash must be deterministic")
	}
	if len(a) != 64 { // SHA-256 hex
		t.Fatalf("hash length = %d, want 64", len(a))
	}
	if HashCode("123456") == HashCode("123457") {
		t.Fatal("different codes must hash differently")
	}
}

func TestNormalizeEmail(t *testing.T) {
	ok := []struct{ in, want string }{
		{"  User@Example.COM ", "user@example.com"},
		{"a.b+c@domain.cd", "a.b+c@domain.cd"},
	}
	for _, tt := range ok {
		got, err := NormalizeEmail(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q, nil", tt.in, got, err, tt.want)
		}
	}
	bad := []string{"", "not-an-email", "a@", "@b.cd", "a b@c.cd", "a@b", string(make([]byte, 300))}
	for _, in := range bad {
		if _, err := NormalizeEmail(in); err == nil {
			t.Errorf("NormalizeEmail(%q) unexpectedly valid", in)
		}
	}
}
