# api

协作平台后端(Go + Gin + pgx + golang-migrate)。当前为 M0 脚手架:健康检查 + PG 连接 + 迁移骨架,业务接口随 M1(账号体系 + 在线保存)落地。

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

迁移文件在 [internal/migrate/migrations/](./internal/migrate/migrations/),使用 [golang-migrate](https://github.com/golang-migrate/migrate) 纯 SQL 格式,**内嵌进二进制、api 启动时自动执行**(部署时 DATABASE_URL 来自 Nacos,独立迁移容器拿不到)。单副本运行前提下安全;未来多副本需加锁或改为外置执行。

本地手动跑(CLI,可选):

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
make migrate-up
```

## 构建

```bash
make build      # 产出 bin/api
docker build -t excalidraw-api .
```
