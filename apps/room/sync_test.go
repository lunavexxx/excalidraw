package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSceneSyncFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" {
			t.Errorf("missing X-Internal-Token")
		}
		if got := r.URL.Query().Get("after"); got != "5" {
			t.Errorf("after = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "2000" {
			t.Errorf("limit = %q", got)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"elements":[{"id":"e1"}],"cursor":9,"has_more":true}}`))
	}))
	defer srv.Close()

	s := NewSceneSync(&Config{GoAPIURL: srv.URL, InternalToken: "tok"})
	patch, err := s.Fetch(context.Background(), testCanvas, 5)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if patch.Cursor != 9 || !patch.HasMore || len(patch.Elements) != 1 {
		t.Fatalf("patch = %+v", patch)
	}
	var el struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(patch.Elements[0], &el); err != nil || el.ID != "e1" {
		t.Fatalf("element = %s", patch.Elements[0])
	}
}

func TestSceneSyncFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"http 403", http.StatusForbidden, ""},
		{"api error", http.StatusOK, `{"code":50000}`},
		{"null elements", http.StatusOK, `{"code":0,"data":{"elements":null}}`},
		{"bad json", http.StatusOK, `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			s := NewSceneSync(&Config{GoAPIURL: srv.URL, InternalToken: "tok"})
			if patch, err := s.Fetch(context.Background(), testCanvas, 0); err == nil {
				t.Fatalf("want error, got %+v", patch)
			}
		})
	}
}

func TestSceneSyncInvalidInput(t *testing.T) {
	s := NewSceneSync(&Config{GoAPIURL: "http://unused", InternalToken: "tok"})
	if _, err := s.Fetch(context.Background(), "bad-id", 0); err == nil {
		t.Error("non-uuid canvas must fail fast")
	}
	if _, err := s.Fetch(context.Background(), testCanvas, -1); err == nil {
		t.Error("negative after must fail fast")
	}
}

func TestParseAfterSeq(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    int64
	}{
		{"valid", map[string]any{"after_seq": float64(12)}, 12},
		{"zero", map[string]any{"after_seq": float64(0)}, 0},
		{"negative", map[string]any{"after_seq": float64(-3)}, 0},
		{"float", map[string]any{"after_seq": 1.5}, 0},
		{"string", map[string]any{"after_seq": "12"}, 0},
		{"missing", map[string]any{}, 0},
		{"nil payload", nil, 0},
		{"non-map", 42, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseAfterSeq(tc.payload); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
