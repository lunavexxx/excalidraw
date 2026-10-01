FROM --platform=${BUILDPLATFORM} node:24@sha256:8530f76a96d88820d288761f022e318970dda93d01536919fbc16076b7983e63 AS build

WORKDIR /opt/node_app

COPY . .

# do not ignore optional dependencies:
# Error: Cannot find module @rollup/rollup-linux-x64-gnu
RUN --mount=type=cache,target=/root/.cache/yarn \
    npm_config_target_arch=${TARGETARCH} yarn --frozen-lockfile --network-timeout 600000

ARG NODE_ENV=production

# api 基址:默认同源 /api/v1(生产经 Caddy /api 转发);local.yml 跨端口时
# 由 compose 传 http://localhost:8080/api/v1 覆盖。值不进 git。
ARG VITE_APP_API_URL=/api/v1
ENV VITE_APP_API_URL=${VITE_APP_API_URL}

# 协作 WS:默认空=同源(经 Caddy /socket.io 反代 room);不设即不会
# 指向任何外部服务器。local.yml 跨端口时由 compose 传 http://localhost:3002。
ARG VITE_APP_WS_SERVER_URL=
ENV VITE_APP_WS_SERVER_URL=${VITE_APP_WS_SERVER_URL}

RUN npm_config_target_arch=${TARGETARCH} yarn build:app:docker

FROM nginx:stable-alpine-slim@sha256:2c605dbeab79a6b2a63340474fe58119d0ef95bdc4b1f41df0aa689659b3d13b

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /opt/node_app/build /usr/share/nginx/html

HEALTHCHECK CMD wget -q -O /dev/null http://localhost || exit 1
