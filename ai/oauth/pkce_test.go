package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestPKCE_GeneratePKCEMatchesS256Spec(t *testing.T) {
	a, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE failed: %v", err)
	}
	b, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("second GeneratePKCE failed: %v", err)
	}
	if a.Verifier == "" || a.Verifier == b.Verifier {
		t.Fatalf("expected non-empty distinct verifiers, got %q and %q", a.Verifier, b.Verifier)
	}
	// 32 random bytes base64url unpadded = 43 chars
	if len(a.Verifier) != 43 {
		t.Fatalf("verifier len = %d, want 43", len(a.Verifier))
	}
	sum := sha256.Sum256([]byte(a.Verifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if a.Challenge != wantChallenge {
		t.Fatalf("challenge = %q, want %q", a.Challenge, wantChallenge)
	}
}
