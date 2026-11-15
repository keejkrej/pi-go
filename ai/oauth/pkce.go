// Ported from packages/ai/src/auth/oauth/pkce.ts (pi v1.5.2).

package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCEPair holds an OAuth 2.0 PKCE code verifier and S256 code challenge.
type PKCEPair struct {
	Verifier  string
	Challenge string
}

// GeneratePKCE generates a PKCE code verifier and SHA-256 code challenge.
func GeneratePKCE() (PKCEPair, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return PKCEPair{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	return PKCEPair{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}
