package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/handlers"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

var (
	version      = "dev"
	buildDate    = "unknown"
	commitHash   = "unknown"
	gitTreeState = "unknown"
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

	log.Info().
		Str("version", version).
		Str("buildDate", buildDate).
		Str("commitHash", commitHash).
		Str("gitTreeState", gitTreeState).
		Msg("Starting portfolio tracker")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	err := run(ctx, getEnv("DB_PATH", "/data/portfolio.db"), getEnv("PORT", "8080"))

	stop()

	if err != nil {
		log.Error().Err(err).Msg("server failed")
		os.Exit(1)
	}
}

// run owns every resource and returns only after its users have stopped.
//
//nolint:contextcheck // Database migration API predates context-aware startup.
func run(ctx context.Context, dbPath, port string) error {
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		closeErr := database.Close()
		if closeErr != nil {
			log.Error().Err(closeErr).Msg("failed to close database")
		}
	}()

	priceService := price.NewService(database, price.DefaultRegistry())
	defer func() {
		closeErr := priceService.Close()
		if closeErr != nil {
			log.Error().Err(closeErr).Msg("failed to stop price refreshes")
		}
	}()

	holdingService := services.NewHoldingService(database, priceService)
	snapshotService := services.NewSnapshotService(database, priceService, holdingService)
	settingsService := services.NewSettingsService(database)
	handler := handlers.New(holdingService, snapshotService, settingsService, priceService)

	snapshotCtx, cancelSnapshots := context.WithCancel(ctx)
	defer cancelSnapshots()

	cronScheduler := cron.New(cron.WithSeconds(), cron.WithChain(cron.Recover(cron.DefaultLogger)))

	_, err = cronScheduler.AddFunc(snapshotSchedule, func() {
		createScheduledSnapshot(snapshotCtx, database, snapshotService.CreateSnapshot)
	})
	if err != nil {
		return fmt.Errorf("add snapshot job: %w", err)
	}

	listenConfig := new(net.ListenConfig)

	listener, err := listenConfig.Listen(ctx, "tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// HTTP requests get their graceful period independently of the signal context.
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()

	tracked := &activeHandlers{handler: handler.SetupRoutes()}
	//nolint:exhaustruct // http.Server has many optional fields
	server := &http.Server{
		Handler:           tracked,
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	cronScheduler.Start()
	log.Info().Str("port", port).Msg("starting server")

	return serve(ctx, server, listener, tracked, cancelRequests,
		cronScheduler, cancelSnapshots, shutdownTimeout)
}

// activeHandlers prevents late-starting requests from touching closed resources.
// Server.Close does not wait for handlers; this explicit barrier does.
type activeHandlers struct {
	handler  http.Handler
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
}

func (tracked *activeHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tracked.mu.Lock()
	if tracked.stopping {
		tracked.mu.Unlock()
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)

		return
	}

	tracked.active.Add(1)

	tracked.mu.Unlock()
	defer tracked.active.Done()

	tracked.handler.ServeHTTP(w, r)
}

func (tracked *activeHandlers) wait() {
	tracked.mu.Lock()
	tracked.stopping = true
	tracked.mu.Unlock()
	tracked.active.Wait()
}

func serve(ctx context.Context, server *http.Server, listener net.Listener,
	tracked *activeHandlers, cancelRequests context.CancelFunc,
	scheduler *cron.Cron, cancelSnapshots context.CancelFunc, timeout time.Duration,
) error {
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()

	var serveErr error

	serveReturned := false

	select {
	case <-ctx.Done():
	case serveErr = <-result:
		serveReturned = true
	}

	cancelSnapshots()

	jobsStopped := scheduler.Stop()

	log.Info().Msg("shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	shutdownErr := server.Shutdown(shutdownCtx)
	// On timeout, cancel cooperative handlers and close their connections, then
	// still wait for their cleanup before the database can be closed.
	cancelRequests()

	if shutdownErr != nil {
		_ = server.Close()
	}

	tracked.wait()

	if !serveReturned {
		serveErr = <-result
	}

	<-jobsStopped.Done()

	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}

	return errors.Join(serveErr, shutdownErr)
}

const (
	snapshotSchedule = "@hourly"
	snapshotTimeout  = 5 * time.Minute
	// SnapshotLockTimeout is the maximum age of a snapshot lock before it is
	// considered stale and reclaimed. It is twice the snapshot timeout so a
	// healthy snapshot (which should finish within snapshotTimeout) is never
	// falsely reclaimed, while a crashed process does not block snapshots
	// indefinitely.
	snapshotLockTimeout = 2 * snapshotTimeout
	readTimeout         = 30 * time.Second
	writeTimeout        = 30 * time.Second
	idleTimeout         = 120 * time.Second
	readHeaderTimeout   = 10 * time.Second
	shutdownTimeout     = 10 * time.Second
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

func createScheduledSnapshot(
	parent context.Context,
	database *sqlx.DB,
	create func(context.Context) error,
) {
	createScheduledSnapshotAt(parent, database, create, time.Now())
}

func createScheduledSnapshotAt(
	parent context.Context,
	database *sqlx.DB,
	create func(context.Context) error,
	now time.Time,
) {
	ctx, cancel := context.WithTimeout(parent, snapshotTimeout)
	defer cancel()

	owner, ok := acquireSnapshotLock(ctx, database)
	if !ok {
		log.Debug().Msg("snapshot lock not acquired, skipping")

		return
	}
	// Deferred release also runs during panic unwinding, before cron recovers.
	defer releaseSnapshotLock(ctx, database, owner)

	slot := now.UTC().Truncate(time.Hour).Unix()
	if !claimSnapshotSlot(ctx, database, slot, owner, now) {
		return
	}
	defer releaseSnapshotSlot(ctx, database, slot, owner)

	err := create(db.WithSnapshotClaim(ctx, slot, owner))
	if err != nil {
		log.Error().Err(err).Msg("failed to create snapshot")
	} else {
		// Production persistence completes this in the insertion transaction.
		// This is idempotent and also supports alternate snapshot callbacks.
		_, err = database.ExecContext(ctx,
			"UPDATE snapshot_runs SET completed = 1 WHERE slot = ? AND owner = ?", slot, owner)
		if err != nil {
			log.Error().Err(err).Msg("failed to complete snapshot slot")

			return
		}

		log.Info().Msg("snapshot created successfully")
	}
}

func acquireSnapshotLock(ctx context.Context, database *sqlx.DB) (string, bool) {
	var random [32]byte

	_, err := rand.Read(random[:])
	if err != nil {
		return "", false
	}

	owner := hex.EncodeToString(random[:])

	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return "", false
	}
	defer tx.Rollback() //nolint:errcheck // Best effort

	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO snapshot_lock (id, acquired_at, owner)
		VALUES (1, ?, ?)
	`, time.Now().UTC(), owner)
	if err != nil {
		return "", false
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return "", false
	}

	if rowsAffected == 0 {
		var acquiredAt time.Time

		queryErr := tx.GetContext(ctx, &acquiredAt,
			"SELECT acquired_at FROM snapshot_lock WHERE id = 1")
		if queryErr != nil {
			return "", false
		}
		// Legacy owner-less rows remain held until the same stale deadline.
		if time.Since(acquiredAt) < snapshotLockTimeout {
			return "", false
		}

		_, err = tx.ExecContext(ctx,
			"UPDATE snapshot_lock SET acquired_at = ?, owner = ? WHERE id = 1",
			time.Now().UTC(), owner)
		if err != nil {
			return "", false
		}
	}

	err = tx.Commit()
	if err != nil {
		return "", false
	}

	return owner, true
}

func releaseSnapshotLock(parent context.Context, database *sqlx.DB, owner string) {
	// Cleanup must survive job cancellation but cannot wait indefinitely.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), shutdownTimeout)
	defer cancel()

	_, err := database.ExecContext(ctx,
		"DELETE FROM snapshot_lock WHERE id = 1 AND owner = ?", owner)
	if err != nil {
		log.Error().Err(err).Msg("failed to release snapshot lock")
	}
}

// Completed UTC slots remain durable after release, so a later replica cannot
// repeat an already successful hour. Failed work releases its claim for retry.
func claimSnapshotSlot(
	ctx context.Context,
	database *sqlx.DB,
	slot int64,
	owner string,
	now time.Time,
) bool {
	result, err := database.ExecContext(ctx, `
		INSERT INTO snapshot_runs (slot, owner, acquired_at) VALUES (?, ?, ?)
		ON CONFLICT(slot) DO UPDATE SET owner = excluded.owner, acquired_at = excluded.acquired_at
		WHERE snapshot_runs.completed = 0 AND snapshot_runs.acquired_at <= ?`,
		slot, owner, now.Unix(), now.Add(-snapshotLockTimeout).Unix())
	if err != nil {
		log.Error().Err(err).Msg("failed to claim snapshot slot")

		return false
	}

	count, err := result.RowsAffected()

	return err == nil && count == 1
}

func releaseSnapshotSlot(parent context.Context, database *sqlx.DB, slot int64, owner string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), shutdownTimeout)
	defer cancel()

	_, err := database.ExecContext(ctx,
		"DELETE FROM snapshot_runs WHERE slot = ? AND owner = ? AND completed = 0", slot, owner)
	if err != nil {
		log.Error().Err(err).Msg("failed to release snapshot slot")
	}
}
