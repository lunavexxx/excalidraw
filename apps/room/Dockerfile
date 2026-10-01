FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
# 服务器(中国机房)直连 proxy.golang.org 超时,默认走国内镜像
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/room .

FROM alpine:3.20
WORKDIR /app
RUN adduser -D -H room && apk add --no-cache ca-certificates
COPY --from=build /out/room /app/room
USER room
EXPOSE 3002
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -qO- http://127.0.0.1:3002/healthz || exit 1
ENTRYPOINT ["/app/room"]
