package auth

import (
	"encoding/hex"
	"testing"
)

// The Reader API key is generated in one place. It used to live in the HTTP
// package; OIDC provisioning needs the same shape (64 lowercase hex chars), and
// two copies would drift — a 32-char key produced by one path would be accepted
// by the reader but not match the length any client documents.
func TestGenerateAPIKeyShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		k, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if len(k) != 64 {
			t.Fatalf("key %q has length %d, want 64", k, len(k))
		}
		if _, err := hex.DecodeString(k); err != nil {
			t.Fatalf("key %q is not lowercase hex: %v", k, err)
		}
		if seen[k] {
			t.Fatalf("GenerateAPIKey repeated %q", k)
		}
		seen[k] = true
	}
}
