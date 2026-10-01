package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/lunavexxx/excalidraw/apps/api/internal/auth"
	"github.com/lunavexxx/excalidraw/apps/api/internal/canvas"
	"github.com/lunavexxx/excalidraw/apps/api/internal/config"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
	"github.com/lunavexxx/excalidraw/apps/api/internal/workspace"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// 必需配置缺失即拒绝启动(fail-fast):静默降级会让配置错误
	// 延迟到请求期才暴露。数据库 schema 不在此处做任何 DDL,
	// 由部署侧手动应用(见 apps/api/migrations/ 与 deploy/README.md)。
	requireConfig(cfg)

	db, err := store.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	phoneCrypto, err := auth.NewPhoneCrypto(cfg.PhoneCryptoKey)
	if err != nil {
		log.Fatalf("phone crypto: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	registerRoutes(router, db, cfg, phoneCrypto)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("api listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	// 事件流折叠:把 canvas_events 按 seq 折叠进 canvas_scenes 快照
	// (到达即落库的持久化主路径,见 internal/canvas/compactor.go)。
	canvas.StartCompactor(ctx, db)

	<-ctx.Done()
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func requireConfig(cfg config.Config) {
	if cfg.DatabaseURL == "" {
		log.Fatal("database_url is required")
	}
	if cfg.JWTSecret == "" {
		log.Fatal("jwt_secret is required")
	}
	if cfg.PhoneCryptoKey == "" {
		log.Fatal("phone_crypto_key is required")
	}
	if cfg.InternalToken == "" {
		log.Fatal("internal_token is required (room ACL 回调依赖)")
	}
}

// registerRoutes 只做路由装配,按模块分组。
func registerRoutes(router *gin.Engine, db *store.DB, cfg config.Config, phoneCrypto *auth.PhoneCrypto) {
	registerHealthRoutes(router, db)
	registerAPIRoutes(router.Group("/api/v1"), db, cfg, phoneCrypto)
}

func registerHealthRoutes(router *gin.Engine, db *store.DB) {
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		if err := db.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unavailable",
				"reason": "database unreachable",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
}

func registerAPIRoutes(apiGroup *gin.RouterGroup, db *store.DB, cfg config.Config, phoneCrypto *auth.PhoneCrypto) {
	registerCORS(apiGroup, cfg.CORSOrigins)

	auth.RegisterRoutes(apiGroup, db, cfg.JWTSecret, phoneCrypto)
	workspace.RegisterRoutes(apiGroup, db, cfg.JWTSecret, phoneCrypto)
	canvas.RegisterRoutes(apiGroup, db, cfg.JWTSecret, canvasLimits(cfg), phoneCrypto)
	canvas.RegisterInternalRoutes(apiGroup, db, cfg.JWTSecret, cfg.InternalToken)
}

// 生产 web 与 api 同站(经 Caddy /api 同源转发),CORS 仅服务
// 本地 docker(web:3000 → api:8080)这类跨端口场景;
// 认证走 Authorization 头,不需要 credentials。
func registerCORS(apiGroup *gin.RouterGroup, origins []string) {
	if len(origins) == 0 {
		return
	}
	apiGroup.Use(cors.New(cors.Config{
		AllowOrigins: origins,
		// canvas 场景/文件用 PUT、重命名/删除用 PATCH/DELETE。
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:       12 * time.Hour,
	}))
}

func canvasLimits(cfg config.Config) canvas.Limits {
	limits := canvas.DefaultLimits()
	if cfg.MaxSceneBytes > 0 {
		limits.MaxSceneBytes = cfg.MaxSceneBytes
	}
	if cfg.MaxFileBytes > 0 {
		limits.MaxFileBytes = cfg.MaxFileBytes
	}
	return limits
}
