package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPublicSessionOmitsCredentials(t *testing.T) {
	s := &Session{Token: "secret-bearer", UserID: "user", DisplayName: "Name", AvatarURL: "avatar"}
	s.setRole(testCanvas, "viewer")
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-bearer") || strings.Contains(string(raw), "Token") {
		t.Fatal("session exposed credentials")
	}
	var p struct {
		UserID string            `json:"user_id"`
		Roles  map[string]string `json:"roles"`
	}
	if json.Unmarshal(raw, &p) != nil || p.UserID != "user" || p.Roles[testCanvas] != "viewer" {
		t.Fatal("public presence missing", string(raw))
	}
}
func TestACLInvalidationAndExpiredToken(t *testing.T) {
	hits := 0
	srv := aclTestServer(t, &hits, http.StatusOK, `{"code":0,"data":{"role":"viewer"}}`)
	defer srv.Close()
	resolver := NewACLResolver(aclTestConfig(t, srv.URL))
	session := &Session{Token: "tk", TokenExp: time.Now().Unix() + 60}
	session.storeACL(testCanvas, "editor", time.Now().Unix()+60)
	if role, _ := resolver.ResolveRole(context.Background(), session, testCanvas); role != "editor" {
		t.Fatal(role)
	}
	session.invalidateACL(testCanvas)
	if role, _ := resolver.ResolveRole(context.Background(), session, testCanvas); role != "viewer" || hits != 1 {
		t.Fatal("role not refreshed", role, hits)
	}
	session.TokenExp = time.Now().Unix() - 1
	if role, _ := resolver.ResolveRole(context.Background(), session, testCanvas); role != "" {
		t.Fatal("expired token used cached access")
	}
}

func TestACLUnavailableIsDistinctFromRevocation(t *testing.T) {
	unavailable := aclTestServer(t, new(int), http.StatusOK, `{"code":50000,"message":"database unavailable"}`)
	defer unavailable.Close()
	resolver := NewACLResolver(aclTestConfig(t, unavailable.URL))
	if role, err := resolver.ResolveAccess(context.Background(), &Session{Token: "tk"}, testCanvas); role != "" || err != ErrACLUnavailable {
		t.Fatalf("unavailable role=%q err=%v", role, err)
	}
	revoked := aclTestServer(t, new(int), http.StatusOK, `{"code":41001,"message":"canvas not found"}`)
	defer revoked.Close()
	resolver = NewACLResolver(aclTestConfig(t, revoked.URL))
	if role, err := resolver.ResolveAccess(context.Background(), &Session{Token: "tk"}, testCanvas); role != "" || err != nil {
		t.Fatalf("revoked role=%q err=%v", role, err)
	}
}
