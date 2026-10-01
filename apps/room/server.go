// 装配:http + socket.io(redis adapter)+ 鉴权 + 协议 + 健康检查,
// 以及优雅关闭链(停收连接 → 注销 Nacos → 冲洗落库队列 → 关资源)。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	goredis "github.com/redis/go-redis/v9"
	sredis "github.com/zishang520/socket.io/adapters/redis/v3"
	radapter "github.com/zishang520/socket.io/adapters/redis/v3/adapter"
	"github.com/zishang520/socket.io/servers/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

type RoomServer struct {
	cfg       *Config
	http      *http.Server
	io        *socket.Server
	persister *Persister
	registry  *Registry
}

func NewRoomServer(cfg *Config) (*RoomServer, error) {
	opts := socket.DefaultServerOptions()
	opts.SetMaxHttpBufferSize(cfg.MaxHTTPBufferSize)
	// 生产经 Caddy 反代 websocket-only(免粘性会话);dev CORS_ORIGINS
	// 未配置时 engine.io 直接握手。
	opts.SetTransports(types.NewSet[socket.TransportCtor](socket.WebSocket))
	opts.SetPingInterval(25 * time.Second)
	opts.SetPingTimeout(20 * time.Second)
	if len(cfg.CORSOrigins) > 0 {
		opts.SetCors(&types.Cors{Origin: cfg.CORSOrigins})
	}

	io := socket.NewServer(nil, opts)

	// 握手鉴权(fail-closed):无效 token 拒绝连接。
	io.Use(func(s *socket.Socket, next func(*types.ExtendedError)) {
		auth := s.Handshake().Auth
		token, _ := auth["token"].(string)
		session := (*Session)(nil)
		if token != "" {
			session = Authenticate(cfg.JWTSecret, token)
		}
		if session == nil {
			next(types.NewExtendedError("invalid token", nil))
			return
		}
		s.SetData(session)
		next(nil)
	})

	// Redis 仅作跨实例 adapter 的 pub/sub 通道,非持久层。
	pubClient, err := newRedisClient(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("redis pub client: %w", err)
	}
	subClient, err := newRedisClient(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("redis sub client: %w", err)
	}
	io.SetAdapter(&radapter.RedisAdapterBuilder{
		Redis: sredis.NewRedisClientWithSub(context.Background(), pubClient, subClient),
		Opts:  radapter.DefaultRedisAdapterOptions(),
	})

	persister, err := NewPersister(cfg.DatabaseURL, cfg.PersistQueueMax)
	if err != nil {
		return nil, err
	}

	rate := NewRateLimiter(cfg.RateLimitPerSecond)
	conns := NewConnectionGuard(cfg.PerIdentityConnectionLimit)
	hub := NewRoomHub(io, cfg, persister, NewSceneSync(cfg), NewACLResolver(cfg), rate, conns)
	hub.Register()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/socket.io/", io.ServeHandler(nil))

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return &RoomServer{cfg: cfg, http: httpSrv, io: io, persister: persister}, nil
}

func newRedisClient(redisURL string) (goredis.UniversalClient, error) {
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis_url: %w", err)
	}
	return goredis.NewClient(opts), nil
}

// Run 启动监听;返回后调用方再执行 Nacos 注册(注册即可服务)。
// 先同步 Listen(绑端口失败立刻暴露,绝不带病注册),再异步 Serve。
func (rs *RoomServer) Run() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", rs.cfg.Port))
	if err != nil {
		return fmt.Errorf("listen :%d: %w", rs.cfg.Port, err)
	}
	go func() {
		if err := rs.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[room] http serve: %v", err)
		}
	}()
	log.Printf("[room] listening on :%d (socket.io + redis adapter ready)", rs.cfg.Port)
	return nil
}

// Shutdown 关闭链(对应 Node 版顺序):停收新连接(socket.io 含
// engine/adapter)→ Nacos 注销 → 冲洗落库队列(5s 内)→ 关 HTTP。
func (rs *RoomServer) Shutdown() {
	log.Printf("[room] shutting down")
	done := make(chan struct{})
	go func() {
		rs.io.Close(func(err error) {
			if err != nil {
				log.Printf("[room] socket.io close: %v", err)
			}
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("[room] socket.io close timed out")
	}
	if rs.registry != nil {
		rs.registry.Deregister()
	}
	rs.persister.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rs.http.Shutdown(ctx); err != nil {
		log.Printf("[room] http shutdown: %v", err)
	}
	log.Printf("[room] bye")
}
