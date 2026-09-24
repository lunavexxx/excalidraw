FROM golang:1.26-alpine AS build

# 国内服务器直连 proxy.golang.org 不可达,统一走 goproxy.cn
ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /api ./cmd/api

# gcr.io 国内不可达,运行层用 alpine(docker.io,可走镜像加速);
# CA 证书从构建阶段复制,运行阶段无需 apk 联网
FROM alpine:3

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /api /api

EXPOSE 8080

ENTRYPOINT ["/api"]
