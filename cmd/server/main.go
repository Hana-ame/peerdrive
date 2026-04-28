package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
)

func main() {
	log.Info("peerdrive base starting...")

	// Load config from environment
	cfg := config.Load()

	// Init database
	if err := repository.InitDB("./peerdrive.db"); err != nil {
		log.Error("db init failed: %v", err)
		os.Exit(1)
	}
	log.Info("database ready")

	// Build router
	r := router.SetupRouter(cfg)

	// Start server with graceful shutdown
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		log.Info("listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error: %v", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Info("stopped")
}
