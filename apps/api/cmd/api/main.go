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
	"github.com/lunavexxx/excalidraw/apps/api/internal/migrate"
	"github.com/lunavexxx/excalidraw/apps/api/internal/store"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	var db *store.DB
	if cfg.DatabaseURL != "" {
		if err := migrate.Run(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrate: %v", err)
		}
		db, err = store.New(context.Background(), cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("connect database: %v", err)
		}
		defer db.Close()
	}

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		if db == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unavailable",
				"reason": "database not configured",
			})
			return
		}
		if err := db.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unavailable",
				"reason": "database unreachable",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	// 生产 web 与 api 同站(经 Caddy /api 同源转发),CORS 仅服务
	// 本地 docker(web:3000 → api:8080)这类跨端口场景。
	// 认证走 Authorization 头,不需要 credentials。
	apiGroup := router.Group("/api/v1")
	if len(cfg.CORSOrigins) > 0 {
		apiGroup.Use(cors.New(cors.Config{
			AllowOrigins: cfg.CORSOrigins,
			// canvas 场景/文件用 PUT、重命名/删除用 PATCH/DELETE。
			AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders: []string{"Authorization", "Content-Type"},
			MaxAge:       12 * time.Hour,
		}))
	}
	if db == nil || cfg.JWTSecret == "" || cfg.PhoneCryptoKey == "" {
		log.Println("auth disabled: need database, jwt_secret and phone_crypto_key")
	} else {
		phoneCrypto, err := auth.NewPhoneCrypto(cfg.PhoneCryptoKey)
		if err != nil {
			log.Fatalf("phone crypto: %v", err)
		}
		auth.RegisterRoutes(apiGroup, db, cfg.JWTSecret, phoneCrypto)

		limits := canvas.DefaultLimits()
		if cfg.MaxSceneBytes > 0 {
			limits.MaxSceneBytes = cfg.MaxSceneBytes
		}
		if cfg.MaxFileBytes > 0 {
			limits.MaxFileBytes = cfg.MaxFileBytes
		}
		canvas.RegisterRoutes(apiGroup, db, cfg.JWTSecret, limits)
	}

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

	<-ctx.Done()
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
