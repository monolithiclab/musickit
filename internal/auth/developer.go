// Package auth mints the two tokens the Apple Music API needs: a developer
// token, signed locally from the MusicKit .p8 key, and a Music-User-Token,
// which only MusicKit JS can produce and therefore requires a browser.
package auth

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"time"
)

// TokenTTL is the lifetime stamped into the developer token. Apple rejects
// anything longer than six months.
const TokenTTL = 180 * 24 * time.Hour

// DeveloperTokenFromFile reads a PKCS#8 .p8 key and signs a developer token.
func DeveloperTokenFromFile(path, teamID, keyID string) (string, error) {
	// #nosec G304 -- the path is the operator's own key, from config or the environment.
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok, err := DeveloperToken(raw, teamID, keyID, TokenTTL)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return tok, nil
}

// DeveloperToken signs an ES256 JWT from the PEM-encoded MusicKit private key.
func DeveloperToken(keyPEM []byte, teamID, keyID string, ttl time.Duration) (string, error) {
	key, err := parseKey(keyPEM)
	if err != nil {
		return "", err
	}

	now := time.Now()
	header := map[string]string{"alg": "ES256", "kid": keyID, "typ": "JWT"}
	payload := map[string]any{
		"iss": teamID,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	h, err := segment(header)
	if err != nil {
		return "", err
	}
	p, err := segment(payload)
	if err != nil {
		return "", err
	}

	signingInput := h + "." + p
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	// JWS wants fixed-width r||s, not the ASN.1 form SignASN1 would produce.
	// Apple rejects the ASN.1 form with an opaque 401.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func parseKey(keyPEM []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("not PEM — expected the .p8 file Apple gave you")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing the private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("holds a %T, expected an ECDSA P-256 key", parsed)
	}
	return key, nil
}

func segment(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
