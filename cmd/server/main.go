package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/handlers"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func main() {
	// Setup structured logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// Configuration
	dbPath := getEnv("DB_PATH", "/data/portfolio.db")
	port := getEnv("PORT", "8080")
	ginMode := getEnv("GIN_MODE", "release")

	gin.SetMode(ginMode)

	// Open database
	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database")
	}
	defer database.Close()

	// Initialize price registry and service
	priceRegistry := price.DefaultRegistry()
	priceService := price.NewService(database, priceRegistry)

	// Initialize services
	holdingService := services.NewHoldingService(database, priceService)
	snapshotService := services.NewSnapshotService(database, priceService, holdingService)
	settingsService := services.NewSettingsService(database)

	// Initialize handlers
	h := handlers.New(holdingService, snapshotService, settingsService, priceService)

	// Setup snapshot cron with leader election
	c := cron.New(cron.WithSeconds())
	_, err = c.AddFunc("0 * * * * *", func() {
		if acquireSnapshotLock(database) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := snapshotService.CreateSnapshot(ctx); err != nil {
				log.Error().Err(err).Msg("failed to create snapshot")
			} else {
				log.Info().Msg("snapshot created successfully")
			}
			releaseSnapshotLock(database)
		} else {
			log.Debug().Msg("snapshot lock not acquired, skipping")
		}
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to add cron job")
	}
	c.Start()
	defer c.Stop()

	// Setup HTTP server
	router := h.SetupRoutes()

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Info().Msg("shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("server shutdown failed")
		}
	}()

	log.Info().Str("port", port).Msg("starting server")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("server failed")
	}

	log.Info().Msg("server stopped")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func acquireSnapshotLock(db *sqlx.DB) bool {
	var result int
	err := db.Get(&result, "SELECT 1 FROM holdings LIMIT 1")
	if err != nil {
		return true // No holdings table yet, allow snapshot
	}

	// Use advisory lock for leader election
	var acquired bool
	err = db.Get(&acquired, "SELECT pg_try_advisory_lock(1234567890)")
	if err != nil {
		// SQLite doesn't have pg_try_advisory_lock, use a simple approach
		// For SQLite, we'll just allow the snapshot (single replica)
		return true
	}
	return acquired
}

func releaseSnapshotLock(db *sqlx.DB) {
	// For SQLite, no-op
	_, _ = db.Exec("SELECT pg_advisory_unlock(1234567890)")
}