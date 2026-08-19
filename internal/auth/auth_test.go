package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newKeyPEM generates a P-256 key in the PKCS#8 PEM shape Apple hands out.
func newKeyPEM(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), key
}

func TestDeveloperTokenIsValidES256(t *testing.T) {
	keyPEM, key := newKeyPEM(t)

	token, err := DeveloperToken(keyPEM, "TEAM123456", "KEY1234567", TokenTTL)
	if err != nil {
		t.Fatalf("DeveloperToken: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}

	var header struct{ Alg, Kid, Typ string }
	decodeSegment(t, parts[0], &header)
	if header.Alg != "ES256" || header.Typ != "JWT" || header.Kid != "KEY1234567" {
		t.Errorf("header = %+v", header)
	}

	var payload struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	decodeSegment(t, parts[1], &payload)
	if payload.Iss != "TEAM123456" {
		t.Errorf("iss = %q, want the Team ID", payload.Iss)
	}
	if payload.Exp <= payload.Iat {
		t.Errorf("exp %d is not after iat %d", payload.Exp, payload.Iat)
	}
	if ttl := time.Duration(payload.Exp-payload.Iat) * time.Second; ttl > 6*30*24*time.Hour {
		t.Errorf("ttl %s exceeds Apple's six-month cap", ttl)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("signature is not base64url: %v", err)
	}
	// The whole point: JWS wants fixed-width r||s. An ASN.1 signature from
	// SignASN1 would be a different length and Apple would answer 401.
	if len(sig) != 64 {
		t.Fatalf("signature is %d bytes, want the 64-byte r||s form (not ASN.1)", len(sig))
	}

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Error("the signature does not verify against the public key")
	}
}

func decodeSegment(t *testing.T, segment string, into any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("segment is not base64url: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("segment is not JSON: %v", err)
	}
}

func TestDeveloperTokenRejectsBadKeys(t *testing.T) {
	if _, err := DeveloperToken([]byte("definitely not pem"), "T", "K", time.Hour); err == nil {
		t.Error("expected an error for non-PEM input")
	}

	rsaLike := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")})
	if _, err := DeveloperToken(rsaLike, "T", "K", time.Hour); err == nil {
		t.Error("expected an error for a PEM block that is not a PKCS#8 key")
	}
}

func TestDeveloperTokenFromFile(t *testing.T) {
	keyPEM, _ := newKeyPEM(t)
	path := filepath.Join(t.TempDir(), "AuthKey.p8")
	if err := os.WriteFile(path, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := DeveloperTokenFromFile(path, "TEAM123456", "KEY1234567"); err != nil {
		t.Fatalf("DeveloperTokenFromFile: %v", err)
	}
	if _, err := DeveloperTokenFromFile(filepath.Join(t.TempDir(), "absent.p8"), "T", "K"); err == nil {
		t.Error("expected an error for a missing key file")
	}
}

func TestTokenCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "user-token")

	if _, ok := CachedToken(path); ok {
		t.Error("an absent cache reported a token")
	}
	if err := SaveToken(path, "tok-123"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	got, ok := CachedToken(path)
	if !ok || got != "tok-123" {
		t.Errorf("CachedToken = %q, %v", got, ok)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cached token is mode %o, want 600 — it is a credential", perm)
	}

	if err := ForgetToken(path); err != nil {
		t.Fatalf("ForgetToken: %v", err)
	}
	if _, ok := CachedToken(path); ok {
		t.Error("the token survived ForgetToken")
	}
	if err := ForgetToken(path); err != nil {
		t.Errorf("ForgetToken on a missing file should be a no-op, got %v", err)
	}
}

func TestCachedTokenIgnoresBlankFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user-token")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := CachedToken(path); ok {
		t.Error("a whitespace-only cache should count as absent")
	}
}

// TestAuthorizerServesPageAndTakesToken drives the whole browser handshake
// with a stub browser: fetch the page, post the token back.
func TestAuthorizerServesPageAndTakesToken(t *testing.T) {
	var served string
	authorizer := &Authorizer{
		Addr:    "127.0.0.1:0",
		Timeout: 10 * time.Second,
		Open: func(url string) error {
			go func() {
				res, err := http.Get(url) // #nosec G107 -- loopback, and the port is chosen by the test.
				if err != nil {
					return
				}
				defer func() { _ = res.Body.Close() }()
				body, _ := io.ReadAll(res.Body)
				served = string(body)

				post, err := http.Post(url+"token", "text/plain", strings.NewReader("  user-token-xyz \n"))
				if err == nil {
					_ = post.Body.Close()
				}
			}()
			return nil
		},
	}

	token, err := authorizer.Token(context.Background(), "dev-token-abc")
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token != "user-token-xyz" {
		t.Errorf("token = %q, want it trimmed", token)
	}
	if !strings.Contains(served, "musickit/v3/musickit.js") {
		t.Error("the page does not load MusicKit JS, which is the only way to mint a user token")
	}
	if !strings.Contains(served, `"dev-token-abc"`) {
		t.Error("the developer token was not quoted into the page")
	}
}

func TestAuthorizerRejectsEmptyToken(t *testing.T) {
	authorizer := &Authorizer{
		Addr:    "127.0.0.1:0",
		Timeout: 10 * time.Second,
		Open: func(url string) error {
			go func() {
				res, err := http.Post(url+"token", "text/plain", strings.NewReader("   "))
				if err == nil {
					_ = res.Body.Close()
				}
			}()
			return nil
		},
	}
	if _, err := authorizer.Token(context.Background(), "dev"); err == nil {
		t.Error("an empty token should be an error, not a cached empty string")
	}
}

func TestAuthorizerHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	authorizer := &Authorizer{
		Addr:    "127.0.0.1:0",
		Timeout: time.Minute,
		Open:    func(string) error { cancel(); return nil },
	}
	_, err := authorizer.Token(ctx, "dev")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestAuthorizerTimesOut(t *testing.T) {
	authorizer := &Authorizer{
		Addr:    "127.0.0.1:0",
		Timeout: 20 * time.Millisecond,
		Open:    func(string) error { return nil },
	}
	_, err := authorizer.Token(context.Background(), "dev")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want a timeout", err)
	}
}

func TestAuthorizerReportsUnusablePort(t *testing.T) {
	authorizer := &Authorizer{Addr: "256.256.256.256:1", Timeout: time.Second}
	if _, err := authorizer.Token(context.Background(), "dev"); err == nil {
		t.Error("expected an error for an address that cannot be bound")
	}
}
