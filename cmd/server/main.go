package main

import (
	"context"
	"errors"
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
	// Setup structured logging.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	//nolint:exhaustruct // zerolog.ConsoleWriter has many optional fields
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:          os.Stderr,
		TimeFormat:   time.RFC3339,
		NoColor:      false,
		TimeLocation: time.UTC,
		PartsOrder: []string{
			zerolog.TimestampFieldName,
			zerolog.LevelFieldName,
			zerolog.MessageFieldName,
		},
	})

	// Configuration.
	dbPath := getEnv("DB_PATH", "/data/portfolio.db")
	port := getEnv("PORT", "8080")
	ginMode := getEnv("GIN_MODE", "release")

	gin.SetMode(ginMode)

	// Open database.
	database, err := db.Open(dbPath)
	if err != nil {
		log.Error().Err(err).Msg("failed to open database")

		return
	}
	defer database.Close()

	// Initialize price registry and service.
	priceRegistry := price.DefaultRegistry()
	priceService := price.NewService(database, priceRegistry)

	// Initialize services.
	holdingService := services.NewHoldingService(database, priceService)
	snapshotService := services.NewSnapshotService(database, priceService, holdingService)
	settingsService := services.NewSettingsService(database)

	// Initialize handlers.
	handler := handlers.New(holdingService, snapshotService, settingsService, priceService)

	// Setup snapshot cron with leader election.
	cronScheduler := cron.New(cron.WithSeconds())

	_, addErr := cronScheduler.AddFunc("0 * * * * *", func() {
		if acquireSnapshotLock(database) {
			ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
			defer cancel()

			snapshotErr := snapshotService.CreateSnapshot(ctx)
			if snapshotErr != nil {
				log.Error().Err(snapshotErr).Msg("failed to create snapshot")
			} else {
				log.Info().Msg("snapshot created successfully")
			}

			releaseSnapshotLock(database)
		} else {
			log.Debug().Msg("snapshot lock not acquired, skipping")
		}
	})
	if addErr != nil {
		log.Error().Err(addErr).Msg("failed to add cron job")

		return
	}

	cronScheduler.Start()
	defer cronScheduler.Stop()

	// Setup HTTP server.
	router := handler.SetupRoutes()

	//nolint:exhaustruct // http.Server has many optional fields
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Info().Msg("shutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		shutdownErr := server.Shutdown(ctx)
		if shutdownErr != nil {
			log.Error().Err(shutdownErr).Msg("server shutdown failed")
		}
	}()

	log.Info().Str("port", port).Msg("starting server")

	listenErr := server.ListenAndServe()
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		log.Error().Err(listenErr).Msg("server failed")

		return
	}

	log.Info().Msg("server stopped")
}

const (
	snapshotTimeout   = 5 * time.Minute
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

func acquireSnapshotLock(database *sqlx.DB) bool {
	var result int

	err := database.Get(&result, "SELECT 1 FROM holdings LIMIT 1")
	if err != nil {
		return true // No holdings table yet, allow snapshot
	}

	// Use a transaction to ensure we use the same connection for the lock check.
	tx, err := database.Beginx()
	if err != nil {
		return false
	}
	defer tx.Rollback() //nolint:errcheck // Best effort

	// Try to insert a lock row; if it succeeds, we got the lock.
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO snapshot_lock (id, acquired_at)
		VALUES (1, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		return false
	}

	// Check if we got the lock (the row was inserted by us).
	rowsAffected, err := res.RowsAffected()
	if err != nil || rowsAffected == 0 {
		return false
	}

	// Commit the transaction to hold the lock.
	err = tx.Commit()

	return err == nil
}

func releaseSnapshotLock(database *sqlx.DB) {
	// Release the lock by deleting the row.
	//nolint:errcheck // Best effort cleanup
	_, _ = database.Exec("DELETE FROM snapshot_lock WHERE id = 1")
}
