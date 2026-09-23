# api

协作平台后端(Go + Gin + pgx + golang-migrate)。当前为 M0 脚手架:健康检查 + PG 连接 + 迁移骨架,业务接口随 M1(账号体系 + 在线保存)落地。

## 本地运行

```bash
go run ./cmd/api            # http://localhost:8080
```

环境变量:

| 变量 | 默认 | 说明 |
|---|---|---|
| `PORT` | `8080` | 监听端口 |
| `DATABASE_URL` | (空) | PostgreSQL 连接串;为空时不连库 |

接口:

- `GET /healthz` — liveness,进程存活即 200
- `GET /readyz` — readiness,PG ping 通过才 200;未配置 DB 或不可达返回 503

## 数据库迁移

迁移文件在 [migrations/](./migrations/),使用 [golang-migrate](https://github.com/golang-migrate/migrate) 纯 SQL 格式:

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
make migrate-up
```

## 构建

```bash
make build      # 产出 bin/api
docker build -t excalidraw-api .
```
