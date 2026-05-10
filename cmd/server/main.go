package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/audit"
	"github.com/carreira-cloud/ai-microservice/internal/cache"
	"github.com/carreira-cloud/ai-microservice/internal/config"
	"github.com/carreira-cloud/ai-microservice/internal/database"
	"github.com/carreira-cloud/ai-microservice/internal/handler"
	"github.com/carreira-cloud/ai-microservice/internal/middleware"
	"github.com/carreira-cloud/ai-microservice/internal/provider/copilot"
	"github.com/carreira-cloud/ai-microservice/internal/repository"
	"github.com/carreira-cloud/ai-microservice/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logrus.WithError(err).Fatal("startup: config load failed")
	}

	// Configure logger.
	if lvl, err := logrus.ParseLevel(cfg.LogLevel); err == nil {
		logrus.SetLevel(lvl)
	}
	logrus.SetFormatter(&logrus.JSONFormatter{})

	// Database.
	db, err := database.Open(cfg)
	if err != nil {
		logrus.WithError(err).Fatal("startup: database open failed")
	}

	// Redis (optional).
	rdb := cache.NewRedisClient(cfg.RedisURL)

	// Wire dependencies.
	auditWorker := audit.NewWorker(db, 100)
	responseCache := cache.NewResponseCache(rdb, cfg.CacheTTLSeconds)
	idemCache := cache.NewIdempotencyCache(rdb)
	limiter := middleware.NewRateLimiter(rdb, cfg.RateLimitRPM)
	prov := copilot.New(cfg.GithubCopilotToken, nil)
	promptRepo := repository.NewPromptRepository(db)
	aiSvc := service.NewAIService(prov, promptRepo, responseCache, idemCache, auditWorker, cfg.CacheTTLSeconds)

	auditWorker.Start()

	// HTTP server.
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	handler.RegisterRoutes(r, aiSvc, promptRepo, limiter, db, rdb, cfg)

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}

	logrus.WithFields(logrus.Fields{
		"port":      cfg.Port,
		"db_driver": cfg.DBDriver,
		"version":   cfg.Version,
		"redis":     rdb != nil,
	}).Info("ai-microservice: starting")

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Fatal("server: listen failed")
		}
	}()

	<-ctx.Done()
	logrus.Info("shutdown: draining...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	auditWorker.Drain(shutCtx)

	logrus.Info("shutdown: complete")
}
