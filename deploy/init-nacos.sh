#!/usr/bin/env bash
# 一次性初始化 Nacos:创建 namespace(local / server)并发布 api 与 room 配置。
# 幂等,可重复执行(重新发布 = 覆盖)。
#
# 用法(在能访问 Nacos 的机器上执行,通常直接在服务器上):
#   NACOS_ADDR=<服务器IP>:8848 SERVER_IP=<服务器IP> WEB_DOMAIN=<正式域名> \
#   NACOS_USERNAME=xxx NACOS_PASSWORD=xxx \
#   PG_PASSWORD=xxx JWT_SECRET=xxx PHONE_CRYPTO_KEY=xxx INTERNAL_TOKEN=xxx \
#   ./init-nacos.sh
# 密钥生成: openssl rand -base64 32;INTERNAL_TOKEN: openssl rand -hex 24
# 注意:重新执行会整体覆盖各配置(api×2 + room×2),保留自定义键值时先在控制台备份。
set -euo pipefail

: "${NACOS_ADDR:?need NACOS_ADDR, e.g. <server-ip>:8848}"
: "${SERVER_IP:?need SERVER_IP}"
: "${WEB_DOMAIN:?need WEB_DOMAIN}"
: "${NACOS_USERNAME:?need NACOS_USERNAME}"
: "${NACOS_PASSWORD:?need NACOS_PASSWORD}"
: "${PG_PASSWORD:?need PG_PASSWORD}"
: "${JWT_SECRET:?need JWT_SECRET (openssl rand -base64 32)}"
: "${PHONE_CRYPTO_KEY:?need PHONE_CRYPTO_KEY (openssl rand -base64 32)}"
: "${INTERNAL_TOKEN:?need INTERNAL_TOKEN (openssl rand -hex 24; 与 deploy/.env 同值)}"

DATA_ID="excalidraw-api.yaml"
GROUP="DEFAULT_GROUP"
PG_USER="excalidraw"
PG_DB="excalidraw"

# server namespace:api 容器在 compose 网络内连 pg:5432
SERVER_DB_URL="postgres://${PG_USER}:${PG_PASSWORD}@pg:5432/${PG_DB}?sslmode=disable"
# local namespace:本地容器走公网连服务器 pg
LOCAL_DB_URL="postgres://${PG_USER}:${PG_PASSWORD}@${SERVER_IP}:5432/${PG_DB}?sslmode=disable"

echo "==> login ${NACOS_ADDR}"
TOKEN=$(curl -fsS -X POST "http://${NACOS_ADDR}/nacos/v1/auth/login" \
  --data-urlencode "username=${NACOS_USERNAME}" \
  --data-urlencode "password=${NACOS_PASSWORD}" |
  sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p')
[ -n "$TOKEN" ] || {
  echo "login failed — 检查 NACOS_USERNAME/NACOS_PASSWORD(首次部署需先在控制台改掉默认 nacos/nacos)"
  exit 1
}

for NS in local server; do
  echo "==> ensure namespace ${NS}"
  # namespace 已存在时接口返回 false,不算错误
  curl -fsS -X POST "http://${NACOS_ADDR}/nacos/v1/console/namespaces" \
    --data-urlencode "accessToken=${TOKEN}" \
    --data-urlencode "customNamespaceId=${NS}" \
    --data-urlencode "namespaceName=${NS}" \
    --data-urlencode "namespaceDesc=${NS}" >/dev/null || true
done

publish() {
  ns=$1
  db_url=$2
  cors=$3
  # 可选键(有代码默认值,按需追加):canvas_scene_max_bytes / canvas_file_max_bytes
  content=$(printf 'port: "8080"\ndatabase_url: "%s"\njwt_secret: "%s"\nphone_crypto_key: "%s"\ncors_origins: "%s"\ninternal_token: "%s"\n' \
    "$db_url" "$JWT_SECRET" "$PHONE_CRYPTO_KEY" "$cors" "$INTERNAL_TOKEN")
  echo "==> publish ${DATA_ID} -> namespace ${ns}"
  ok=$(curl -fsS -X POST "http://${NACOS_ADDR}/nacos/v1/cs/configs" \
    --data-urlencode "accessToken=${TOKEN}" \
    --data-urlencode "tenant=${ns}" \
    --data-urlencode "dataId=${DATA_ID}" \
    --data-urlencode "group=${GROUP}" \
    --data-urlencode "content=${content}")
  [ "$ok" = "true" ] || { echo "publish to ${ns} failed: ${ok}"; exit 1; }
}

# server:web 与 api 同站(经 Caddy /api 同源转发),CORS 仅作为兜底
publish server "$SERVER_DB_URL" "https://${WEB_DOMAIN}"
# local:local.yml 的 web(3000) → api(8080) 跨端口,需要 CORS
publish local "$LOCAL_DB_URL" "http://localhost:3000"

# ── room 配置(dataid excalidraw-room.yaml):socket.io 协作房间服务 ──
# 密钥与 api 同源(JWT_SECRET 校验握手 token;INTERNAL_TOKEN 回调内部端点),
# 此后改密钥只需改这里 + 重启 room,不再需要动 compose env。
publish_room() {
  ns=$1
  db_url=$2
  cors=$3
  content=$(printf 'port: "3002"\nredis_url: "redis://redis:6379"\ndatabase_url: "%s"\njwt_secret: "%s"\ngo_api_url: "http://api:8080/api/v1"\ninternal_token: "%s"\ncors_origins: "%s"\n' \
    "$db_url" "$JWT_SECRET" "$INTERNAL_TOKEN" "$cors")
  echo "==> publish excalidraw-room.yaml -> namespace ${ns}"
  ok=$(curl -fsS -X POST "http://${NACOS_ADDR}/nacos/v1/cs/configs" \
    --data-urlencode "accessToken=${TOKEN}" \
    --data-urlencode "tenant=${ns}" \
    --data-urlencode "dataId=excalidraw-room.yaml" \
    --data-urlencode "group=${GROUP}" \
    --data-urlencode "content=${content}")
  [ "$ok" = "true" ] || { echo "publish room to ${ns} failed: ${ok}"; exit 1; }
}

publish_room server "$SERVER_DB_URL" "https://${WEB_DOMAIN}"
# local:web(3000)/vite dev(5173) 直连 room(3002) 跨端口
publish_room local "$LOCAL_DB_URL" "http://localhost:3000,http://localhost:5173"

echo "==> done. 重启 api/room 生效: docker compose -f test.yml restart api room"
