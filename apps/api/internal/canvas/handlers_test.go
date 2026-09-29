package canvas

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/apiresp"
)

func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"画布 A", true},
		{"a", true},
		{"", false},
		{"   ", false},
		{string(make([]rune, 100)), true},
		{string(make([]rune, 101)), false},
	}
	for _, tc := range cases {
		if got := validName(tc.name); got != tc.want {
			t.Fatalf("validName(%q...) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// create 的名字归一化:空白(含空串/纯空白)视为省略,交给 store 填默认名;
// 超过 100 rune 拒绝。
func TestNormalizeCreateName(t *testing.T) {
	cases := []struct {
		name     string
		want     string
		wantCode bool
	}{
		{"", "", true},
		{"   ", "", true},
		{"  画布 A  ", "画布 A", true},
		{string(make([]rune, 100)), string(make([]rune, 100)), true},
		{string(make([]rune, 101)), "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeCreateName(tc.name)
		if ok != tc.wantCode || got != tc.want {
			t.Fatalf("normalizeCreateName(%q...) = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.wantCode)
		}
	}
}

func TestParsePositiveInt(t *testing.T) {
	if n, err := parsePositiveInt("20"); err != nil || n != 20 {
		t.Fatalf("parsePositiveInt(20) = %d, %v", n, err)
	}
	for _, bad := range []string{"0", "-1", "abc", "", "1.5"} {
		if _, err := parsePositiveInt(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

// 未带 Bearer token 的请求应被 AuthRequired 拦下,按契约回 40100(HTTP 200)。
func TestRegisterRoutesRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), nil, "test-secret", DefaultLimits())

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/canvases", nil))

	var body apiresp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not apiresp json: %v", err)
	}
	if body.Code != apiresp.CodeUnauthorized {
		t.Fatalf("code = %d, want %d", body.Code, apiresp.CodeUnauthorized)
	}
}
