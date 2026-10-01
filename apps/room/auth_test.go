package main

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 表测试用例与 apps/api/internal/auth 对齐,锁住两处 claim 解析不漂移。
func sign(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func TestAuthenticateAccessRoundTrip(t *testing.T) {
	token := sign(t, "secret", jwt.MapClaims{
		"typ": "access",
		"sub": "user-1",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	session := Authenticate("secret", token)
	if session == nil || session.UserID != "user-1" || session.Guest != nil {
		t.Fatalf("session = %+v", session)
	}
	if session.Identity() != "user-1" {
		t.Errorf("identity = %q, want user-1", session.Identity())
	}
}

func TestAuthenticateGuestRoundTrip(t *testing.T) {
	token := sign(t, "secret", jwt.MapClaims{
		"typ":  "guest",
		"sub":  "g:link-1:abcdef",
		"cid":  "canvas-1",
		"role": "viewer",
		"exp":  float64(time.Now().Add(time.Hour).Unix()),
	})
	session := Authenticate("secret", token)
	if session == nil || session.Guest == nil {
		t.Fatalf("session = %+v", session)
	}
	g := session.Guest
	if g.ShareLinkID != "link-1" || g.CanvasID != "canvas-1" || g.Role != "viewer" {
		t.Errorf("guest = %+v", g)
	}
	if g.Subject != "g:link-1:abcdef" || session.Identity() != g.Subject {
		t.Errorf("identity = %q", session.Identity())
	}
}

func TestAuthenticateRejects(t *testing.T) {
	valid := jwt.MapClaims{"typ": "access", "sub": "u", "exp": float64(time.Now().Add(time.Hour).Unix())}
	cases := []struct {
		name  string
		token string
	}{
		{"garbage", "not.a.token"},
		{"wrong secret", sign(t, "secret-a", valid)},
		{"wrong typ", sign(t, "secret", jwt.MapClaims{"typ": "refresh", "sub": "u", "exp": float64(time.Now().Add(time.Hour).Unix())})},
		{"empty sub", sign(t, "secret", jwt.MapClaims{"typ": "access", "sub": "", "exp": float64(time.Now().Add(time.Hour).Unix())})},
		{"expired", sign(t, "secret", jwt.MapClaims{"typ": "access", "sub": "u", "exp": float64(time.Now().Add(-time.Hour).Unix())})},
		{"guest missing cid", sign(t, "secret", jwt.MapClaims{"typ": "guest", "sub": "g:l:j", "role": "editor", "exp": float64(time.Now().Add(time.Hour).Unix())})},
		{"guest bad role", sign(t, "secret", jwt.MapClaims{"typ": "guest", "sub": "g:l:j", "cid": "c", "role": "owner", "exp": float64(time.Now().Add(time.Hour).Unix())})},
		{"guest bad subject no prefix", sign(t, "secret", jwt.MapClaims{"typ": "guest", "sub": "l:j", "cid": "c", "role": "viewer", "exp": float64(time.Now().Add(time.Hour).Unix())})},
		{"guest bad subject no jti", sign(t, "secret", jwt.MapClaims{"typ": "guest", "sub": "g:l:", "cid": "c", "role": "viewer", "exp": float64(time.Now().Add(time.Hour).Unix())})},
		{"guest bad subject empty link", sign(t, "secret", jwt.MapClaims{"typ": "guest", "sub": "g::j", "cid": "c", "role": "viewer", "exp": float64(time.Now().Add(time.Hour).Unix())})},
		{"guest bad subject no colon", sign(t, "secret", jwt.MapClaims{"typ": "guest", "sub": "g:lj", "cid": "c", "role": "viewer", "exp": float64(time.Now().Add(time.Hour).Unix())})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if session := Authenticate("secret", tc.token); session != nil {
				t.Errorf("want reject, got %+v", session)
			}
		})
	}
}

func TestAuthenticateAcceptsMissingExp(t *testing.T) {
	// Node 版仅校验 typ/sub 等显式 claim,exp 缺省放行(tokenExp=0 → ACL TTL 回落)。
	token := sign(t, "secret", jwt.MapClaims{"typ": "access", "sub": "user-1"})
	session := Authenticate("secret", token)
	if session == nil || session.TokenExp != 0 {
		t.Fatalf("session = %+v", session)
	}
}

func TestSessionRoleAndACLCache(t *testing.T) {
	s := &Session{}
	if got := s.roleOf("c1"); got != "" {
		t.Errorf("initial role = %q", got)
	}
	s.setRole("c1", "viewer")
	s.setRole("c2", "editor")
	if s.roleOf("c1") != "viewer" || s.roleOf("c2") != "editor" {
		t.Fatalf("roles not stored per-room")
	}

	now := time.Now().Unix()
	s.storeACL("c1", "editor", now+60)
	if role, ok := s.cachedACL("c1", now); !ok || role != "editor" {
		t.Fatalf("cached = %q %v", role, ok)
	}
	if _, ok := s.cachedACL("c1", now+61); ok {
		t.Error("expired entry must not be returned")
	}
	if _, ok := s.cachedACL("missing", now); ok {
		t.Error("missing entry must not be returned")
	}
}
