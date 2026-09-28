package apiresp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}

func TestOKShape(t *testing.T) {
	c, rec := newTestContext(t)
	OK(c, gin.H{"user": "u1"})

	if rec.Code != http.StatusOK {
		t.Errorf("http status = %d, want 200", rec.Code)
	}
	var body Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != CodeOK || body.Message != "ok" {
		t.Errorf("body = %+v", body)
	}
	if body.Data == nil {
		t.Error("data missing")
	}
	if body.Timestamp <= 0 || body.Timestamp > time.Now().UnixMilli()+1000 {
		t.Errorf("timestamp = %d, want unix ms near now", body.Timestamp)
	}
}

func TestFailShape(t *testing.T) {
	c, rec := newTestContext(t)
	Fail(c, CodePhoneTaken, "phone already registered")

	if rec.Code != http.StatusOK {
		t.Errorf("http status = %d, want 200", rec.Code)
	}
	var body Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != CodePhoneTaken {
		t.Errorf("code = %d, want %d", body.Code, CodePhoneTaken)
	}
	if body.Data != nil {
		t.Errorf("data = %v, want null", body.Data)
	}
	if body.Timestamp <= 0 {
		t.Errorf("timestamp = %d", body.Timestamp)
	}
}

func TestFailDataIsNullInJSON(t *testing.T) {
	c, rec := newTestContext(t)
	Fail(c, CodeInvalidRequest, "bad")
	// data 键必须存在且为 null(前端依赖字段齐全)
	if got := rec.Body.String(); !strings.Contains(got, `"data":null`) {
		t.Errorf("body = %s, want data:null", got)
	}
}
