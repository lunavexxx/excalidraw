# deploy — 部署与环境

单机部署,两个环境:

| 环境 | 文件 | 跑什么 | 说明 |
|---|---|---|---|
| **正式** | `test.yml` | pg + nacos + api + web + caddy | 服务器上,对外服务 |
| **测试(本地)** | `local.yml` | api + web | 开发机上,连服务器的 nacos/pg |

> 文件名沿用项目早期叫法;`test.yml` = 服务器正式环境的部署文件。
>
> **`local.yml` / `test.yml` 不进 git**(部署拓扑私有,各机器自持一份);仓库里保留的是本文档、Caddyfile、`.env` 模板和 init-nacos.sh。
>
> **本文档中 `<服务器IP>`、`<域名>` 均为占位符**,实际值只存在于各机器的 `deploy/.env`(`SERVER_IP` / `WEB_DOMAIN` / `API_DOMAIN`),不进 git。

```
服务器 <服务器IP>(test.yml)
┌────────────────────────────────────────────┐
│ Caddy 80/443(自动 HTTPS)                  │
│  ├ <域名> / www.<域名>                     │
│  │   ├ /api/*  → api(同源转发,无 CORS)   │
│  │   └ 其余    → web                       │
│  └ api.<域名>        → api(运维直连)      │
│ web(nginx 静态)                           │
│ api(Gin)← 配置来自 Nacos(namespace server)│
│ room(Go,socket.io 协作)← 配置/注册同上    │
│ nacos(standalone + 鉴权;8848/9848)        │
│ pg(5432)                                  │
└────────────────────────────────────────────┘
        ▲ 5432                 ▲ 8848 + 9848
┌───────┴─────────────────────┴──────────────┐
│ 本地(local.yml)                            │
│ api 127.0.0.1:8080(namespace local)        │
│ room 127.0.0.1:3002(可 --scale room=2)    │
│ web 127.0.0.1:3000                         │
└────────────────────────────────────────────┘
```

**配置管理**:api 与 room 的全部应用配置存 Nacos,通过 `--nacos-addr` 参数定位;`.env`(gitignore)只存基础设施自举密钥(pg 密码、nacos 自身鉴权)与部署位置(IP、域名)。改配置不进 git、不用重新构建镜像。

## 子域名与 DNS

| 域名 | 服务 | 状态 |
|---|---|---|
| `<域名>`(裸域) | web | 已指向服务器(conduit 旧配置) |
| `www.<域名>` | 跳转裸域 | 已指向服务器,conduit 删除后由 Caddy 接管 |
| `api.<域名>` | api | **需新增 A 记录 → `<服务器IP>`** |
| `files.<域名>` | MinIO 文件直链 | M1 预留 |
| `room.<域名>` | WebSocket 协作 | M3 预留 |
| `admin.<域名>` | 管理后台 | M7 预留 |

实际值:`.env` 的 `WEB_DOMAIN`(裸域)/ `API_DOMAIN`(API 子域),经环境变量注入 caddy 容器,Caddyfile 中以 `{$WEB_DOMAIN}` 等引用。

## 服务器一次性初始化

### 1. 删除 conduit(三件套,已确认废弃)

```bash
docker ps -a                          # 找出 conduit 相关容器(conduit / element-web / 反代)
cd <conduit 的 compose 目录>
docker-compose down -v                # 连卷一起删,释放 80/8080
docker rmi <相关镜像>                  # 或事后 docker image prune -a 前先确认
docker volume prune && docker network prune
rm -rf <conduit 目录>                 # compose / 配置目录
ss -tlnp | grep -E ':80|:8080'        # 确认端口已释放(应无输出)
```

www 的旧证书不用迁移,Caddy 会自动重签。机器上的 K8s(apiserver 6443 / etcd)保持不动,与本栈隔离。

### 2. 升级 Docker / 安装 compose v2

服务器目前 Docker 20.10 + docker-compose v1(`name:` 等语法不支持),本部署要求 compose v2:

```bash
# Debian 12,按 Docker 官方源安装 docker-ce + docker-compose-plugin
apt-get update && apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list
apt-get update && apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
docker compose version               # 确认 v2
```

### 3. `/etc/docker/daemon.json`(镜像加速 + 日志轮转)

国内直连 docker.io 会超时,必须配加速;磁盘紧,日志统一限量:

```json
{
  "registry-mirrors": ["https://<你的阿里云个人加速器地址>"],
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true
}
```

阿里云加速器地址:控制台 → 容器镜像服务 → 镜像加速器。改完 `systemctl restart docker`。

### 4. 加 2G swap(服务器无 swap)

```bash
fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab
```

### 5. 安全组(阿里云控制台)

| 端口 | 开放范围 | 用途 |
|---|---|---|
| 80, 443 | 0.0.0.0/0 | Caddy 对外服务 |
| 5432 | **仅本机开发机出口 IP** | 本地 api 连 pg |
| 8848, 9848 | **仅本机开发机出口 IP** | 本地 api 连 Nacos(HTTP + gRPC) |

## 首次部署(服务器)

```bash
git clone https://github.com/lunavexxx/excalidraw.git && cd excalidraw/deploy
cp .env.example .env && vim .env     # 填全部值(含 SERVER_IP / WEB_DOMAIN / API_DOMAIN)
# test.yml 不在仓库里,先在开发机上传一份:
#   scp deploy/test.yml root@<服务器IP>:~/excalidraw/deploy/
docker compose -f test.yml up -d --build
```

栈起来后(首次约几分钟,nacos JVM 启动要几十秒):

1. **改 Nacos 管理员密码**:浏览器开 `http://<服务器IP>:8848/nacos`(安全组已限 IP),默认 `nacos/nacos`,改掉后回填 `.env` 的 `NACOS_USERNAME/NACOS_PASSWORD`
2. **初始化配置**:
   ```bash
   NACOS_ADDR=<服务器IP>:8848 SERVER_IP=<服务器IP> WEB_DOMAIN=<域名> \
   NACOS_USERNAME=xxx NACOS_PASSWORD=xxx \
   PG_PASSWORD=<与 .env 相同> \
   JWT_SECRET=$(openssl rand -base64 32) \
   PHONE_CRYPTO_KEY=$(openssl rand -base64 32) \
   INTERNAL_TOKEN=$(openssl rand -hex 24) \
   ./init-nacos.sh
   docker compose -f test.yml restart api room
   ```
   > 密钥生成后同步回填 `.env` 存档(Nacos 控制台看不到 base64 原文时以 .env 为准);
   > 重跑 init-nacos.sh 会整体覆盖配置,自定义键值先备份。
3. **验证**:
   ```bash
   curl https://api.<域名>/healthz    # 200
   curl https://api.<域名>/readyz     # 200(Nacos 配置 + pg 连通)
   # 浏览器打开 https://<域名> 见白板
   ```

## 数据库迁移(手动)

api 启动**不做任何 DDL**;schema 变更以 SQL 文件形式提供在 `apps/api/migrations/`(0001 起、按序号递增,`.up.sql` 应用 / `.down.sql` 回退)。**新迁移需要手动应用**,两种方式任选:

```bash
# 方式 A:在服务器上(已 git pull,经 pg 容器执行)
docker compose -f test.yml exec -T pg \
  psql -U excalidraw -d excalidraw < apps/api/migrations/<NNNN_xxx>.up.sql

# 方式 B:从本机直连公网 5432(安全组已放行时)
psql "postgres://excalidraw:${PG_PASSWORD}@<服务器IP>:5432/excalidraw" \
  -f apps/api/migrations/<NNNN_xxx>.up.sql
```

- 全新环境从 0001 开始按序执行;只加新迁移时执行对应文件即可
- 判断迁移是否已应用以库内对象为准(`\dt` 看表);库里的 `schema_migrations`
  是早期 golang-migrate 的遗留,可忽略
- 也可用 golang-migrate CLI(见 `apps/api/Makefile` 的 `migrate-up/down`)

## 日常更新(服务器)

```bash
git pull && docker compose -f test.yml up -d --build
```

涉及 schema 变更的版本需先按上一节手动应用迁移,再更新容器。磁盘吃紧时 `docker system prune -f` 清 dangling 层。

## 本地环境(local.yml)

前置:服务器栈已运行(本地 api 依赖服务器的 Nacos 和 pg)。

```bash
colima start                          # docker 环境;若拉不动镜像,先给 colima 配 registry mirror
cd deploy && cp .env.example .env     # 与服务器同值(NACOS_USERNAME/PASSWORD 必须一致)
docker compose -f local.yml up -d --build
curl http://localhost:8080/readyz     # 200 = 连通服务器 Nacos(local namespace)+ pg
# 浏览器打开 http://localhost:3000
```

要热更新开发时仍然原生跑:`apps/web` 用 `corepack yarn start`,`apps/api` 用 `go run ./cmd/api`(不传 `--nacos-addr` 时走 PORT/DATABASE_URL 环境变量)。

## Nacos 配置管理

- 配置位置:namespace `server`(服务器 api/room)/ `local`(本地 api/room);dataId:`excalidraw-api.yaml` + `excalidraw-room.yaml`(协作房间服务,socket.io 线协议,persist-then-relay)
- 改配置:Nacos 控制台改 yaml → `docker compose -f test.yml restart api room`(配置只在启动时拉取)
- **改 PG 密码要同时改两处**:`.env`(pg 容器)和 Nacos 里的 `database_url`(api/room 两份),然后 `up -d` + `restart api room`
- **改 JWT_SECRET / INTERNAL_TOKEN**:只改 Nacos(api、room 两份 dataid)→ 重启 api 与 room;compose 不再注入这两个密钥,无双源问题
- room 还会把自己注册到 Nacos naming(服务名 `excalidraw-room`,临时实例,gRPC 保活,控制台「服务管理 → 服务列表」可见)
- 备份即 `nacos-data` 卷(derby 内嵌存储)

## 秘密清单

| 项 | 存放 | 说明 |
|---|---|---|
| `database_url`(含 PG 密码) | Nacos | 应用配置唯一来源(api/room 两份) |
| `jwt_secret` | Nacos | api access token 签名,room 握手验签(`.env` 的 `JWT_SECRET` 发布进去) |
| `phone_crypto_key` | Nacos | 手机号加解密/HMAC 主密钥,**一经使用不可更换** |
| `PG_PASSWORD` | `.env`(gitignore) | pg 容器启动;与 Nacos 保持一致 |
| `SERVER_IP` / `WEB_DOMAIN` / `API_DOMAIN` | `.env` | 部署位置与域名,不进 git |
| `NACOS_AUTH_*` | `.env` | nacos 容器自身鉴权 |
| `NACOS_USERNAME/PASSWORD` | `.env` | api/room SDK + init 脚本登录 |
| `JWT_SECRET` / `PHONE_CRYPTO_KEY` | `.env` | 应用密钥存档(经 init-nacos.sh 发布进 Nacos) |

## 故障排查

- **api 起不来,日志 `get config from nacos`** → Nacos 没就绪/凭据错/没跑 init-nacos.sh;api 内置 90s 重试,凭据错误则直接失败
- **证书签发失败** → DNS 未生效/安全组没开 80、443;`docker compose -f test.yml logs caddy`
- **拉镜像超时** → daemon.json 没配加速器;GitHub 慢可配 git 代理或用 gitee 镜像
- **pg 连不上(本地)** → 安全组 5432 没放行你的 IP;密码两处不一致

## 已知边界(按计划推进)

- 账号体系已自建(`/api/v1/auth/*`,手机号+密码,web 经 Caddy 同源访问);在线保存/文档 API/图片上传随 M1 余项落地,此前 web 前端的历史云功能仍指向上游后端
- room 协作服务已落地(Go,`apps/room`;socket.io 线协议 + Nacos 配置/服务注册);`room.<域名>` 仍为预留——生产流量走主域 `https://<域名>/socket.io/*`(Caddy 反代到 room:3002),无需独立子域
- MinIO / admin / 备份脚本分别随 M1 余项 / M7 落地
- 数据库迁移为手动步骤(schema 与 api 启动解耦),见「数据库迁移」一节
