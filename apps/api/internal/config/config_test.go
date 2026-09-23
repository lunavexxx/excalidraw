package config

import "testing"

func TestParseConfigYAML(t *testing.T) {
	content := "port: \"9090\"\ndatabase_url: postgres://u:p@pg:5432/excalidraw\n"
	cfg, err := parseConfigYAML(content)
	if err != nil {
		t.Fatalf("parseConfigYAML: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want 9090", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://u:p@pg:5432/excalidraw" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestParseConfigYAMLPortDefault(t *testing.T) {
	cfg, err := parseConfigYAML("database_url: postgres://u:p@pg:5432/excalidraw\n")
	if err != nil {
		t.Fatalf("parseConfigYAML: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want default 8080", cfg.Port)
	}
}

func TestParseConfigYAMLInvalid(t *testing.T) {
	if _, err := parseConfigYAML("port: ["); err == nil {
		t.Fatal("want error for invalid yaml")
	}
}

func TestParseFlags(t *testing.T) {
	opts, err := parseFlags([]string{"--nacos-addr=nacos:8848", "--nacos-namespace=server"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.addr != "nacos:8848" {
		t.Errorf("addr = %q", opts.addr)
	}
	if opts.namespace != "server" {
		t.Errorf("namespace = %q", opts.namespace)
	}
	if opts.dataID != defaultDataID {
		t.Errorf("dataID = %q, want default %q", opts.dataID, defaultDataID)
	}
	if opts.group != defaultGroup {
		t.Errorf("group = %q, want default %q", opts.group, defaultGroup)
	}
}

func TestParseFlagsEmpty(t *testing.T) {
	opts, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.addr != "" {
		t.Errorf("addr = %q, want empty (env fallback)", opts.addr)
	}
}

func TestParseAddr(t *testing.T) {
	cases := []struct {
		in    string
		host  string
		port  int
		fails bool
	}{
		{in: "nacos", host: "nacos", port: 8848},
		{in: "nacos:8848", host: "nacos", port: 8848},
		{in: "203.0.113.10:8848", host: "203.0.113.10", port: 8848},
		{in: "nacos:bogus", fails: true},
		{in: ":8848", fails: true},
		{in: "bad:addr:8848", fails: true},
	}
	for _, tc := range cases {
		host, port, err := parseAddr(tc.in)
		if tc.fails {
			if err == nil {
				t.Errorf("parseAddr(%q): want error, got %q %d", tc.in, host, port)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAddr(%q): %v", tc.in, err)
			continue
		}
		if host != tc.host || port != tc.port {
			t.Errorf("parseAddr(%q) = %q %d, want %q %d", tc.in, host, port, tc.host, tc.port)
		}
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("PORT", "9000")
	t.Setenv("DATABASE_URL", "postgres://foo")
	cfg := loadFromEnv()
	if cfg.Port != "9000" || cfg.DatabaseURL != "postgres://foo" {
		t.Errorf("loadFromEnv = %+v", cfg)
	}
}
