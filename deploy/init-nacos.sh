#!/usr/bin/env bash
# 一次性初始化 Nacos:创建 namespace(local / server)并发布 api 配置。
# 幂等,可重复执行(重新发布 = 覆盖)。
#
# 用法(在能访问 Nacos 的机器上执行,通常直接在服务器上):
#   NACOS_ADDR=<服务器IP>:8848 SERVER_IP=<服务器IP> \
#   NACOS_USERNAME=xxx NACOS_PASSWORD=xxx \
#   PG_PASSWORD=xxx ./init-nacos.sh
set -euo pipefail

: "${NACOS_ADDR:?need NACOS_ADDR, e.g. <server-ip>:8848}"
: "${SERVER_IP:?need SERVER_IP}"
: "${NACOS_USERNAME:?need NACOS_USERNAME}"
: "${NACOS_PASSWORD:?need NACOS_PASSWORD}"
: "${PG_PASSWORD:?need PG_PASSWORD}"

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
  content=$(printf 'port: "8080"\ndatabase_url: "%s"\n' "$db_url")
  echo "==> publish ${DATA_ID} -> namespace ${ns}"
  ok=$(curl -fsS -X POST "http://${NACOS_ADDR}/nacos/v1/cs/configs" \
    --data-urlencode "accessToken=${TOKEN}" \
    --data-urlencode "tenant=${ns}" \
    --data-urlencode "dataId=${DATA_ID}" \
    --data-urlencode "group=${GROUP}" \
    --data-urlencode "content=${content}")
  [ "$ok" = "true" ] || { echo "publish to ${ns} failed: ${ok}"; exit 1; }
}

publish server "$SERVER_DB_URL"
publish local "$LOCAL_DB_URL"

echo "==> done. 重启 api 生效: docker compose -f test.yml restart api"
