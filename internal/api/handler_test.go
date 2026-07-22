package api

import (
	"encoding/base64"
	"testing"
)

func TestGenerateNonce_ValidBase64OfExpectedLength(t *testing.T) {
	nonce, err := generateNonce()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		t.Fatalf("nonce is not valid base64: %v", err)
	}
	if len(decoded) != 16 {
		t.Fatalf("expected 16 decoded bytes, got %d", len(decoded))
	}
}

func TestGenerateNonce_IsUniquePerCall(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		nonce, err := generateNonce()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, dup := seen[nonce]; dup {
			t.Fatalf("nonce repeated across calls: %q", nonce)
		}
		seen[nonce] = struct{}{}
	}
}
