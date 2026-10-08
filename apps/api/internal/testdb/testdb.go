// Package testdb creates an isolated schema when COLLAB_TEST_DATABASE_URL is set.
package testdb

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

func URL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("COLLAB_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("COLLAB_TEST_DATABASE_URL not configured")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("collab_test_%d", time.Now().UnixNano())
	schema := pgx.Identifier{name}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = conn.Close(context.Background())
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(file), "../../migrations/*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		sql, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(file), err)
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", name)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}
