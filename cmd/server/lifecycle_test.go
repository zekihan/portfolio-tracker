package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/robfig/cron/v3"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestRunStartupFailures(t *testing.T) {
	t.Parallel()

	var err error

	err = run(
		t.Context(),
		filepath.Join(t.TempDir(), "absent", "db.sqlite"),
		"127.0.0.1", "0",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "open database") {
		t.Fatalf("expected database startup error, got %v", err)
	}

	// Occupy the same loopback listener used by native deployments.
	listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	_, portString, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err = run(
		ctx,
		filepath.Join(t.TempDir(), "db.sqlite"),
		"127.0.0.1", portString,
	)
	if err == nil ||
		!strings.Contains(err.Error(), "listen") {
		t.Fatalf("expected listen startup error, got %v", err)
	}
}

//nolint:gocognit,gocyclo // Integration test deliberately coordinates both workers through shutdown.
func TestServeWaitsForHandlersAndCron(t *testing.T) {
	t.Parallel()

	for _, forceTimeout := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "graceful", true: "timeout"}[forceTimeout],
			func(t *testing.T) {
				t.Parallel()

				database, err := db.Open(filepath.Join(t.TempDir(), "db.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer database.Close()

				ctx, stop := context.WithCancel(t.Context())
				defer stop()

				requestCtx, cancelRequests := context.WithCancel(t.Context())
				defer cancelRequests()

				jobCtx, cancelJobs := context.WithCancel(t.Context())
				defer cancelJobs()

				handlerStarted := make(chan struct{})
				finishHandler := make(chan struct{})
				handlerDone := make(chan error, 1)
				tracked := &activeHandlers{
					handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						close(handlerStarted)

						if forceTimeout {
							<-r.Context().Done()
						}

						<-finishHandler

						handlerDone <- database.PingContext(context.WithoutCancel(r.Context()))

						w.WriteHeader(http.StatusNoContent)
					}),
				}
				server := &http.Server{
					Handler:           tracked,
					ReadHeaderTimeout: time.Second,
					BaseContext:       func(net.Listener) context.Context { return requestCtx },
				}

				listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}

				cronStarted := make(chan struct{})
				cronCancelled := make(chan struct{})
				finishCron := make(chan struct{})
				cronDone := make(chan error, 1)
				scheduler := cron.New(cron.WithSeconds())

				_, err = scheduler.AddFunc("@every 1s", func() {
					close(cronStarted)
					<-jobCtx.Done()
					close(cronCancelled)
					<-finishCron

					cronDone <- database.PingContext(context.WithoutCancel(jobCtx))
				})
				if err != nil {
					t.Fatal(err)
				}

				scheduler.Start()

				done := make(chan error, 1)

				timeout := 5 * time.Second
				if forceTimeout {
					timeout = 30 * time.Millisecond
				}

				go func() {
					serveErr := serve(
						ctx,
						server,
						listener,
						tracked,
						cancelRequests,
						scheduler,
						cancelJobs,
						timeout,
					)
					_ = database.Close()

					done <- serveErr
				}()

				clientDone := make(chan struct{})
				go func() {
					defer close(clientDone)

					request, requestErr := http.NewRequestWithContext(
						t.Context(),
						http.MethodGet,
						"http://"+listener.Addr().String(),
						http.NoBody,
					)
					if requestErr != nil {
						return
					}

					response, responseErr := http.DefaultClient.Do(request)
					if responseErr == nil {
						_ = response.Body.Close()
					}
				}()

				await(t, handlerStarted)
				await(t, cronStarted)
				stop()
				await(t, cronCancelled)

				select {
				case resultErr := <-done:
					t.Fatalf("returned while workers active: %v", resultErr)
				default:
				}

				close(finishHandler)

				err = <-handlerDone
				if err != nil {
					t.Fatalf("database closed during handler: %v", err)
				}

				select {
				case resultErr := <-done:
					t.Fatalf("returned while cron active: %v", resultErr)
				default:
				}

				close(finishCron)

				err = <-cronDone
				if err != nil {
					t.Fatalf("database closed during cron: %v", err)
				}

				select {
				case err := <-done:
					if forceTimeout && !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("expected timeout, got %v", err)
					}

					if !forceTimeout && err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("shutdown stuck")
				}

				await(t, clientDone)
			},
		)
	}
}

func await(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for worker")
	}
}

func TestSnapshotReleaseOnlyOwnLease(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	first, ok := acquireSnapshotLock(t.Context(), database)
	if !ok {
		t.Fatal("first acquisition failed")
	}

	_, err = database.Exec(
		"UPDATE snapshot_lock SET acquired_at = ? WHERE id = 1",
		time.Now().Add(-2*snapshotLockTimeout),
	)
	if err != nil {
		t.Fatal(err)
	}

	second, ok := acquireSnapshotLock(t.Context(), database)
	if !ok || first == second {
		t.Fatal("stale lock did not receive a new owner")
	}

	releaseSnapshotLock(t.Context(), database, first)

	if _, ok := acquireSnapshotLock(t.Context(), database); ok {
		t.Fatal("stale owner deleted successor's lease")
	}

	releaseSnapshotLock(t.Context(), database, second)

	if _, ok := acquireSnapshotLock(t.Context(), database); !ok {
		t.Fatal("current owner could not release lease")
	}
}

func TestSnapshotPanicReleasesLease(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic")
			}
		}()

		createScheduledSnapshot(
			t.Context(),
			database,
			func(context.Context) error { panic("test snapshot failure") },
		)
	}()

	if _, ok := acquireSnapshotLock(t.Context(), database); !ok {
		t.Fatal("panic stranded snapshot lease")
	}
}

func TestSnapshotLegacyLeaseAndCancellation(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	_, err = database.Exec("INSERT INTO snapshot_lock (id) VALUES (1)")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := acquireSnapshotLock(t.Context(), database); ok {
		t.Fatal("stole fresh legacy lease")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, ok := acquireSnapshotLock(ctx, database); ok {
		t.Fatal("canceled job acquired lease")
	}
}

func TestMainStartupFailureExitsNonzero(t *testing.T) {
	if os.Getenv("PORTFOLIO_TEST_MAIN_FAILURE") == "1" {
		main()

		return
	}

	t.Parallel()
	//nolint:gosec // Launch this test binary with a fixed test selector to inspect main exit status.
	command := exec.CommandContext(
		t.Context(),
		os.Args[0],
		"-test.run=^TestMainStartupFailureExitsNonzero$",
	)
	command.Env = append(os.Environ(), "PORTFOLIO_TEST_MAIN_FAILURE=1",
		"DB_PATH="+filepath.Join(t.TempDir(), "absent", "db.sqlite"))
	output, err := command.CombinedOutput()

	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("expected exit 1, got %v; output: %s", err, output)
	}
}

func TestSnapshotLockConcurrentInstances(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "db.sqlite")

	first, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = first.Close() })

	second, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = second.Close() })

	start := make(chan struct{})
	acquired := make(chan bool, 2)

	for _, database := range []*sqlx.DB{first, second} {
		go func() {
			<-start

			_, ok := acquireSnapshotLock(t.Context(), database)
			acquired <- ok
		}()
	}

	close(start)

	winners := 0

	for range 2 {
		if <-acquired {
			winners++
		}
	}

	if winners != 1 {
		t.Fatalf("concurrent acquisitions = %d, want exactly 1", winners)
	}
}

func TestStoppedHandlersRejectLateRequests(t *testing.T) {
	t.Parallel()

	called := false
	tracked := &activeHandlers{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})}
	tracked.wait()

	recorder := httptest.NewRecorder()
	tracked.ServeHTTP(
		recorder,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
	)

	if called || recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("late request reached handler: %v, status: %d", called, recorder.Code)
	}
}

func TestScheduledSnapshotCancellationReleasesLease(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{})

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		createScheduledSnapshot(ctx, database, func(jobCtx context.Context) error {
			close(started)
			<-jobCtx.Done()

			return jobCtx.Err()
		})
	}()

	await(t, started)
	cancel()
	await(t, stopped)

	if _, ok := acquireSnapshotLock(t.Context(), database); !ok {
		t.Fatal("canceled snapshot did not release its lease")
	}
}
