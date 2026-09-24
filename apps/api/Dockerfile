FROM golang:1.26-alpine AS build

# 国内服务器直连 proxy.golang.org 不可达,统一走 goproxy.cn
ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /api ./cmd/api

FROM gcr.io/distroless/static-debian12

COPY --from=build /api /api

EXPOSE 8080

ENTRYPOINT ["/api"]
