package main

import (
	"os"
	"strings"
	"testing"
)

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("PORT", "3100")
	t.Setenv("REDIS_URL", "redis://r:6379")
	t.Setenv("DATABASE_URL", "postgres://db")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("GO_API_URL", "http://api:8080/api/v1/")
	t.Setenv("INTERNAL_TOKEN", "tok")
	t.Setenv("CORS_ORIGINS", "http://a, http://b ,,")
	t.Setenv("RATE_LIMIT_PER_SECOND", "10")
	t.Setenv("PER_USER_CONNECTION_LIMIT", "0") // 非法值回落默认
	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := requireConfig(cfg); err != nil {
		t.Fatalf("requireConfig: %v", err)
	}
	if cfg.Port != 3100 || cfg.RateLimitPerSecond != 10 || cfg.PerIdentityConnectionLimit != defaultConnLimitPerIdentity {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.GoAPIURL != "http://api:8080/api/v1" {
		t.Errorf("go_api_url trailing slash not stripped: %q", cfg.GoAPIURL)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Errorf("cors = %v", cfg.CORSOrigins)
	}
	if cfg.ServiceName != "excalidraw-room" {
		t.Errorf("service name default: %q", cfg.ServiceName)
	}
}

func TestLoadFromEnvMissingRequired(t *testing.T) {
	for _, env := range []string{"REDIS_URL", "DATABASE_URL", "JWT_SECRET", "GO_API_URL", "INTERNAL_TOKEN"} {
		t.Setenv(env, "")
	}
	_, err := Load([]string{})
	if err == nil || !strings.Contains(err.Error(), "missing required config") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseConfigYAML(t *testing.T) {
	content := `
port: "3002"
redis_url: "redis://redis:6379"
database_url: "postgres://pg/excalidraw"
jwt_secret: "sec"
go_api_url: "http://api:8080/api/v1"
internal_token: "it"
cors_origins: "http://localhost:3000"
max_http_buffer_size: 8388608
rate_limit_per_second: 42
`
	cfg, err := parseConfigYAML(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Port != 3002 || cfg.MaxHTTPBufferSize != 8388608 || cfg.RateLimitPerSecond != 42 || cfg.PersistQueueMax != defaultPersistQueueMax {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(cfg.CORSOrigins) != 1 {
		t.Errorf("cors = %v", cfg.CORSOrigins)
	}
}

func TestParseConfigYAMLEmptyUsesDefaults(t *testing.T) {
	cfg, err := parseConfigYAML(`
redis_url: "redis://r"
database_url: "postgres://d"
jwt_secret: "s"
go_api_url: "http://api"
internal_token: "t"
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Port != defaultPort || cfg.MaxHTTPBufferSize != defaultMaxHTTPBufferSize {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestParseAddr(t *testing.T) {
	if host, port, _ := parseAddr("nacos"); host != "nacos" || port != 8848 {
		t.Error("bare host should default port 8848")
	}
	if host, port, _ := parseAddr("1.2.3.4:9999"); host != "1.2.3.4" || port != 9999 {
		t.Error("host:port parse failed")
	}
	if _, _, err := parseAddr(":8848"); err == nil {
		t.Error("empty host must error")
	}
	if _, _, err := parseAddr("h:x"); err == nil {
		t.Error("bad port must error")
	}
}

func TestEnvIntFallback(t *testing.T) {
	t.Setenv("X_N", "7")
	if envInt("X_N", 3) != 7 {
		t.Error("valid env int not read")
	}
	t.Setenv("X_N", "-2")
	if envInt("X_N", 3) != 3 {
		t.Error("non-positive must fall back")
	}
	t.Setenv("X_N", "abc")
	if envInt("X_N", 3) != 3 {
		t.Error("unparsable must fall back")
	}
	os.Unsetenv("X_N")
	if envInt("X_N", 3) != 3 {
		t.Error("missing must fall back")
	}
}
