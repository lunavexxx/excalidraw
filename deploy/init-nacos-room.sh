#!/usr/bin/env bash
# 仅发布 room 配置(dataid excalidraw-room.yaml,server/local 双 namespace)。
#
# 与 init-nacos.sh 的区别:不重发 api/phone 配置,零密钥输入——jwt_secret、
# internal_token、database_url 直接从各 namespace 现有的 excalidraw-api.yaml
# 读取(与 api 同源);顺带修复 api yaml 的历史键名 bug(大写 INTERNAL_TOKEN,
# 新版 api 解析的是小写键,不修则 git pull 后 api 拒启)。修复仅改键名,值不变。
#
# 用法:
#   NACOS_ADDR=<服务器IP>:8848 NACOS_USERNAME=xxx NACOS_PASSWORD=xxx \
#   WEB_DOMAIN=<正式域名> ./init-nacos-room.sh [--dry-run]
# WEB_DOMAIN 仅用作 server namespace 的 cors_origins 兜底。
# 幂等,可重复执行(重新发布 = 覆盖)。
set -euo pipefail

: "${NACOS_ADDR:?need NACOS_ADDR, e.g. <server-ip>:8848}"
: "${NACOS_USERNAME:?need NACOS_USERNAME}"
: "${NACOS_PASSWORD:?need NACOS_PASSWORD}"
WEB_DOMAIN="${WEB_DOMAIN:-}"
case "${1:-}" in
  --dry-run) DRY_RUN="dry" ;;
  "")        DRY_RUN="apply" ;;
  *) echo "unknown flag: $1 (use --dry-run)"; exit 1 ;;
esac

GROUP="DEFAULT_GROUP"
API_DATA_ID="excalidraw-api.yaml"
ROOM_DATA_ID="excalidraw-room.yaml"
BASE="http://${NACOS_ADDR}"

TOKEN=$(curl -fsS -m 10 -X POST "${BASE}/nacos/v1/auth/login" \
  --data-urlencode "username=${NACOS_USERNAME}" \
  --data-urlencode "password=${NACOS_PASSWORD}" |
  sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p')
[ -n "$TOKEN" ] || { echo "login failed — 检查 NACOS_USERNAME/NACOS_PASSWORD"; exit 1; }

# extract 从 yaml 文本按大小写不敏感的键取值(值内不含引号,直接剥引号)
extract() { # <key> <content>
  printf '%s\n' "$2" | grep -i "^$1:" | head -1 | sed -E 's/^[^:]*:[[:space:]]*//' | tr -d '"'
}

mask() { printf '%.4s****' "$1"; }

publish_room() { # <ns> <content>
  ns=$1 content=$2
  if [ "$DRY_RUN" = "dry" ]; then
    echo "==> [dry-run] ${ROOM_DATA_ID} -> namespace ${ns}"
    printf '%s\n' "$content" | sed -E \
      -e 's/^(jwt_secret: ".{4}).*(")$/\1****\2/' \
      -e 's/^(internal_token: ".{4}).*(")$/\1****\2/' \
      -e 's/^(database_url: "postgres:\/\/[^:]+:)[^@]+(@)/\1****\2/'
    return 0
  fi
  ok=$(curl -fsS -m 10 -X POST "${BASE}/nacos/v1/cs/configs" \
    --data-urlencode "accessToken=${TOKEN}" \
    --data-urlencode "tenant=${ns}" \
    --data-urlencode "dataId=${ROOM_DATA_ID}" \
    --data-urlencode "group=${GROUP}" \
    --data-urlencode "content=${content}")
  [ "$ok" = "true" ] || { echo "publish room to ${ns} failed: ${ok}"; exit 1; }
  echo "==> published ${ROOM_DATA_ID} -> namespace ${ns}"
}

fix_api_key_case() { # <ns> <content>
  if ! printf '%s\n' "$2" | grep -q '^INTERNAL_TOKEN:'; then
    return 0
  fi
  fixed=$(printf '%s\n' "$2" | sed 's/^INTERNAL_TOKEN:/internal_token:/')
  if [ "$DRY_RUN" = "dry" ]; then
    echo "==> [dry-run] ${API_DATA_ID} -> namespace $1: 大写 INTERNAL_TOKEN 键名将修为小写(值不变)"
    return 0
  fi
  ok=$(curl -fsS -m 10 -X POST "${BASE}/nacos/v1/cs/configs" \
    --data-urlencode "accessToken=${TOKEN}" \
    --data-urlencode "tenant=$1" \
    --data-urlencode "dataId=${API_DATA_ID}" \
    --data-urlencode "group=${GROUP}" \
    --data-urlencode "content=${fixed}")
  [ "$ok" = "true" ] || { echo "fix ${API_DATA_ID} in $1 failed: ${ok}"; exit 1; }
  echo "==> fixed ${API_DATA_ID} -> namespace $1(INTERNAL_TOKEN 键名改小写,值不变;重启 api 生效)"
}

for NS in server local; do
  echo "==> namespace ${NS}"
  api_content=$(curl -fsS -m 10 "${BASE}/nacos/v1/cs/configs?dataId=${API_DATA_ID}&group=${GROUP}&tenant=${NS}&accessToken=${TOKEN}") || {
    echo "    跳过:${API_DATA_ID} 不存在(先跑 init-nacos.sh 初始化)"; continue
  }
  [ -n "$(printf '%s\n' "$api_content" | tr -d '[:space:]')" ] || {
    echo "    跳过:${API_DATA_ID} 为空"; continue
  }
  fix_api_key_case "$NS" "$api_content"

  jwt=$(extract jwt_secret "$api_content")
  itok=$(extract internal_token "$api_content")
  db=$(extract database_url "$api_content")
  [ -n "$jwt" ] && [ -n "$itok" ] && [ -n "$db" ] || {
    echo "    错误:${NS} 的 ${API_DATA_ID} 缺 jwt_secret/internal_token/database_url,无法派生 room 配置"; exit 1
  }

  case "$NS" in
    server) cors="https://${WEB_DOMAIN}" ;;
    *)      cors="http://localhost:3000,http://localhost:5173" ;;
  esac
  room_content=$(printf 'port: "3002"\nredis_url: "redis://redis:6379"\ndatabase_url: "%s"\njwt_secret: "%s"\ngo_api_url: "http://api:8080/api/v1"\ninternal_token: "%s"\ncors_origins: "%s"\n' \
    "$db" "$jwt" "$itok" "$cors")
  publish_room "$NS" "$room_content"
done

echo "==> done. 重启生效: docker compose -f test.yml restart api room"
