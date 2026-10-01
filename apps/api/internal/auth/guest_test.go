package auth

import (
	"testing"
	"time"
)

func TestGuestTokenRoundtrip(t *testing.T) {
	const secret = "test-secret"
	token, expiresIn, err := SignGuestToken(secret, "link-1", "canvas-1", "editor", 0)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if expiresIn != int(guestTokenTTL.Seconds()) {
		t.Fatalf("expiresIn = %d, want %d", expiresIn, int(guestTokenTTL.Seconds()))
	}
	guest, err := ParseGuestToken(secret, token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if guest.ShareLinkID != "link-1" || guest.CanvasID != "canvas-1" || guest.Role != "editor" {
		t.Fatalf("guest = %+v", guest)
	}
	if guest.Subject == "" {
		t.Fatal("subject should be set")
	}
}

func TestGuestTokenRejects(t *testing.T) {
	const secret = "test-secret"
	if _, _, err := SignGuestToken(secret, "", "canvas-1", "viewer", 0); err == nil {
		t.Fatal("empty share link should fail")
	}
	if _, _, err := SignGuestToken(secret, "link-1", "canvas-1", "admin", 0); err == nil {
		t.Fatal("invalid role should fail")
	}

	token, _, err := SignGuestToken(secret, "link-1", "canvas-1", "viewer", 0)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ParseGuestToken("wrong-secret", token); err == nil {
		t.Fatal("wrong secret should fail")
	}

	// access token 不是合法 guest token。
	access, _, err := SignAccessToken(secret, "user-1")
	if err != nil {
		t.Fatalf("sign access: %v", err)
	}
	if _, err := ParseGuestToken(secret, access); err == nil {
		t.Fatal("access token should not parse as guest")
	}
}

func TestParseIdentityDispatch(t *testing.T) {
	const secret = "test-secret"
	access, _, err := SignAccessToken(secret, "user-1")
	if err != nil {
		t.Fatalf("sign access: %v", err)
	}
	ident, err := ParseIdentity(secret, access)
	if err != nil || ident.UserID != "user-1" || ident.Guest != nil {
		t.Fatalf("access identity = %+v, err %v", ident, err)
	}

	guestToken, _, err := SignGuestToken(secret, "link-1", "canvas-1", "viewer", time.Minute)
	if err != nil {
		t.Fatalf("sign guest: %v", err)
	}
	ident, err = ParseIdentity(secret, guestToken)
	if err != nil || ident.UserID != "" || ident.Guest == nil {
		t.Fatalf("guest identity = %+v, err %v", ident, err)
	}
	if ident.Guest.CanvasID != "canvas-1" {
		t.Fatalf("guest canvas = %q", ident.Guest.CanvasID)
	}

	if _, err := ParseIdentity(secret, "garbage"); err == nil {
		t.Fatal("garbage token should fail")
	}
}

func TestSplitGuestSubject(t *testing.T) {
	for _, bad := range []string{"", "user-1", "g:", "g:link", "x:link:jti"} {
		if _, _, err := splitGuestSubject(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
	subject, linkID, err := splitGuestSubject("g:link-1:abcdef")
	if err != nil || subject != "g:link-1:abcdef" || linkID != "link-1" {
		t.Fatalf("split = %q, %q, %v", subject, linkID, err)
	}
}
