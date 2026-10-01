package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func aclTestServer(t *testing.T, hits *int, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if r.Header.Get("X-Internal-Token") != "tok" {
			t.Errorf("missing X-Internal-Token")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tk" {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func aclTestConfig(t *testing.T, apiURL string) *Config {
	t.Helper()
	return &Config{GoAPIURL: apiURL, InternalToken: "tok"}
}

func TestACLResolveAllowed(t *testing.T) {
	hits := 0
	srv := aclTestServer(t, &hits, http.StatusOK, `{"code":0,"data":{"role":"editor"}}`)
	defer srv.Close()
	r := NewACLResolver(aclTestConfig(t, srv.URL))
	session := &Session{Token: "tk"}
	role, err := r.ResolveRole(context.Background(), session, testCanvas)
	if err != nil || role != "editor" {
		t.Fatalf("role=%q err=%v", role, err)
	}
	// 第二次走缓存,不回源。
	if _, err := r.ResolveRole(context.Background(), session, testCanvas); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1 (cached)", hits)
	}
}

func TestACLFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"http 500", http.StatusInternalServerError, `{"code":0,"data":{"role":"owner"}}`},
		{"api error code", http.StatusOK, `{"code":42002,"message":"forbidden"}`},
		{"missing role", http.StatusOK, `{"code":0,"data":{}}`},
		{"unknown role", http.StatusOK, `{"code":0,"data":{"role":"admin"}}`},
		{"bad json", http.StatusOK, `not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			srv := aclTestServer(t, &hits, tc.status, tc.body)
			defer srv.Close()
			r := NewACLResolver(aclTestConfig(t, srv.URL))
			session := &Session{Token: "tk"}
			role, err := r.ResolveRole(context.Background(), session, testCanvas)
			if err != nil || role != "" {
				t.Errorf("must fail closed, got role=%q err=%v", role, err)
			}
			// 拒绝结果同样缓存:第二次不回源。
			if _, err := r.ResolveRole(context.Background(), session, testCanvas); err != nil {
				t.Fatal(err)
			}
			if hits != 1 {
				t.Errorf("hits = %d, want 1", hits)
			}
		})
	}
}

func TestACLCacheTTLRespectsTokenExpiry(t *testing.T) {
	srv := aclTestServer(t, new(int), http.StatusOK, `{"code":0,"data":{"role":"viewer"}}`)
	defer srv.Close()
	r := NewACLResolver(aclTestConfig(t, srv.URL))
	// token 30s 后过期:缓存 TTL 应取 30s 而非 60s。
	session := &Session{Token: "tk", TokenExp: time.Now().Unix() + 30}
	if _, err := r.ResolveRole(context.Background(), session, testCanvas); err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	entry := session.acl[testCanvas]
	session.mu.Unlock()
	if entry.expires-time.Now().Unix() > 31 {
		t.Errorf("ttl = %d, want <= 30", entry.expires-time.Now().Unix())
	}
}

func TestACLConnectionErrorFailsClosed(t *testing.T) {
	srv := aclTestServer(t, new(int), http.StatusOK, `{"code":0,"data":{"role":"editor"}}`)
	r := NewACLResolver(aclTestConfig(t, srv.URL))
	srv.Close() // 直接关闭,回调必然失败
	role, err := r.ResolveRole(context.Background(), &Session{Token: "tk"}, testCanvas)
	if role != "" {
		t.Errorf("must fail closed on connection error, got %q", role)
	}
	_ = err
}
