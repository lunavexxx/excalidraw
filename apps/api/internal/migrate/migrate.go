// Package migrate 在 api 启动时应用内嵌的 SQL 迁移,部署侧无需手动跑迁移
// (DATABASE_URL 来自 Nacos,迁移容器拿不到)。
package migrate

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Run 应用所有待执行的 up 迁移;schema 已是最新时为 no-op。
// 以单副本运行为前提(compose 不做 scale),多副本时需加锁或外置迁移。
func Run(databaseURL string) error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, toPgxURL(databaseURL))
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// toPgxURL 把 postgres:// 换成 golang-migrate pgx/v5 driver 注册的 pgx5:// scheme。
func toPgxURL(databaseURL string) string {
	if strings.HasPrefix(databaseURL, "postgres://") {
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgres://")
	}
	return databaseURL
}
