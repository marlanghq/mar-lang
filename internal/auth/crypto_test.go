package auth

import (
	"strings"
	"testing"
)

func TestCodeShape(t *testing.T) {
	c, err := Code(6)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if len(c) != 6 {
		t.Fatalf("Code: expected length 6, got %d (%q)", len(c), c)
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			t.Fatalf("Code: non-digit %q in %q", r, c)
		}
	}
}

func TestCodeLengthClamp(t *testing.T) {
	short, _ := Code(1)
	if len(short) != 4 {
		t.Errorf("Code(1) should clamp to 4, got %d", len(short))
	}
	long, _ := Code(99)
	if len(long) != 10 {
		t.Errorf("Code(99) should clamp to 10, got %d", len(long))
	}
}

func TestTokenIsURLSafe(t *testing.T) {
	tok, err := Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	// 32 random bytes → 43 base64url chars (no padding).
	if len(tok) != 43 {
		t.Fatalf("Token: expected 43 chars, got %d", len(tok))
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("Token: contains non-URL-safe chars: %q", tok)
	}
}

func TestHashDeterministic(t *testing.T) {
	a := Hash("secret", "abc")
	b := Hash("secret", "abc")
	if a != b {
		t.Fatalf("Hash should be deterministic for same input")
	}
	c := Hash("other", "abc")
	if a == c {
		t.Fatalf("Hash should change with secret")
	}
}

func TestEqualConstantTime(t *testing.T) {
	if !Equal("x", "x") {
		t.Errorf("Equal(x,x) should be true")
	}
	if Equal("x", "y") {
		t.Errorf("Equal(x,y) should be false")
	}
	if Equal("x", "xx") {
		t.Errorf("Equal across different lengths should be false")
	}
}
