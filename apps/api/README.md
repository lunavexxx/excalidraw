# api

协作平台后端(Go + Gin + pgx)。健康检查 + PG 连接 + 账号/画布/工作区/协作 ACL 等 API;数据库 schema 变更以 SQL 文件提供、由部署侧手动应用(见下)。

## 配置来源

两种模式,二选一:

**1. Nacos 配置中心(部署用)** — 传 `--nacos-addr` 即启用,完整配置(PORT、DATABASE_URL)从 Nacos 拉取:

```bash
./api --nacos-addr=<nacos-host>:8848 --nacos-namespace=local
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `--nacos-addr` | (空) | `host[:port]`,端口缺省 8848;为空则走环境变量模式 |
| `--nacos-namespace` | (空) | Nacos namespace ID(`local` / `server`) |
| `--nacos-dataid` | `excalidraw-api.yaml` | 配置 data ID |
| `--nacos-group` | `DEFAULT_GROUP` | 配置 group |

Nacos 客户端凭据走环境变量(避免出现在命令行):`NACOS_USERNAME`、`NACOS_PASSWORD`。

Nacos 中的配置格式(yaml):

```yaml
port: "8080"
database_url: postgres://excalidraw:PASSWORD@pg:5432/excalidraw?sslmode=disable
```

**2. 环境变量(本地脱机调试)** — 不传 `--nacos-addr` 即走此模式:

| 变量 | 默认 | 说明 |
|---|---|---|
| `PORT` | `8080` | 监听端口 |
| `DATABASE_URL` | (空) | PostgreSQL 连接串;为空时不连库 |

```bash
go run ./cmd/api            # http://localhost:8080
```

接口:

- `GET /healthz` — liveness,进程存活即 200
- `GET /readyz` — readiness,PG ping 通过才 200;未配置 DB 或不可达返回 503

## 数据库迁移

迁移文件在 [migrations/](./migrations/)(golang-migrate 纯 SQL 格式:`0001_init` → `0005_canvas_events`,`.up.sql` 应用 / `.down.sql` 回退)。**api 启动不做任何 DDL**——schema 由部署侧按序手动应用,流程与命令见 [deploy/README.md](../deploy/README.md)「数据库迁移」一节。

本机装了 golang-migrate CLI 时可用 make 快捷方式(可选):

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
make migrate-up   # DATABASE_URL 默认指向 localhost,可覆盖
```

## 构建

```bash
make build      # 产出 bin/api
docker build -t excalidraw-api .
```
