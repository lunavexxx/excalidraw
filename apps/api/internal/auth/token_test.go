package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAccessTokenRoundTrip(t *testing.T) {
	token, expiresIn, err := SignAccessToken("secret", "user-1")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	if expiresIn != int(accessTokenTTL.Seconds()) {
		t.Errorf("expiresIn = %d, want %d", expiresIn, int(accessTokenTTL.Seconds()))
	}
	sub, err := ParseAccessToken("secret", token)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if sub != "user-1" {
		t.Errorf("sub = %q, want user-1", sub)
	}
}

func TestAccessTokenWrongSecret(t *testing.T) {
	token, _, err := SignAccessToken("secret-a", "user-1")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	if _, err := ParseAccessToken("secret-b", token); err == nil {
		t.Error("want error for wrong secret")
	}
}

func TestParseAccessTokenRejectsGarbage(t *testing.T) {
	if _, err := ParseAccessToken("secret", "not.a.token"); err == nil {
		t.Error("want error for garbage token")
	}
}

func TestRefreshTokenShape(t *testing.T) {
	raw, hash, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	if len(raw) != 43 { // 32 bytes base64url, no padding
		t.Errorf("raw len = %d, want 43", len(raw))
	}
	if strings.ContainsAny(raw, "+/=") {
		t.Errorf("raw contains non-url-safe chars: %q", raw)
	}
	if len(hash) != 32 {
		t.Errorf("hash len = %d, want 32", len(hash))
	}
	back, err := HashRefreshToken(raw)
	if err != nil {
		t.Fatalf("HashRefreshToken: %v", err)
	}
	if string(back) != string(hash) {
		t.Error("HashRefreshToken(raw) mismatch with NewRefreshToken hash")
	}
}

func TestHashRefreshTokenRejectsMalformed(t *testing.T) {
	if _, err := HashRefreshToken("short"); err == nil {
		t.Error("want error for malformed token")
	}
}

func TestAccessTokenExpiry(t *testing.T) {
	claims := accessClaims{
		Typ: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * accessTokenTTL)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-accessTokenTTL)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	if _, err := ParseAccessToken("secret", token); err == nil {
		t.Error("want error for expired token")
	}
}

func TestParseAccessTokenRejectsWrongTyp(t *testing.T) {
	claims := jwt.RegisteredClaims{
		Subject:   "user-1",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ParseAccessToken("secret", token); err == nil {
		t.Error("want error for token without typ=access")
	}
}
