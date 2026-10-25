package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

const (
	restoreWorkerArgument    = "--portfolio-restore-worker"
	restoreWorkerTimeout     = 20 * time.Second
	restoreWorkerCPUSeconds  = 5
	restoreWorkerMemoryBytes = 256 << 20
)

// One worker per server process bounds aggregate restore resource use. Waiting
// for this slot never acquires the application's sole SQLite connection.
var restoreWorkerSlot = make(chan struct{}, 1)

// The same executable is also the worker in distroless containers. Dispatch
// before server startup (and before test flag parsing in test executables).
func init() {
	if !isRestoreWorkerProcess() {
		return
	}

	code := 1

	err := runRestoreWorker(os.Args[2], os.Args[3])
	if err == nil {
		code = 0
	}

	//nolint:forbidigo // Exit only the dedicated worker before normal server startup.
	os.Exit(code)
}

func isRestoreWorkerProcess() bool {
	return len(os.Args) == 4 && os.Args[1] == restoreWorkerArgument
}

func runRestoreWorker(source, destination string) error {
	err := limitRestoreWorker()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), restoreWorkerTimeout)
	defer cancel()

	database, err := db.Open(destination)
	if err != nil {
		return fmt.Errorf("open clean restore database: %w", err)
	}

	defer func() { _ = database.Close() }()
	// Only the clean output can be written. SQLite scratch stays in memory, the
	// output has a fixed page budget, and no WAL or rollback-journal files grow.
	_, err = database.ExecContext(
		ctx,
		`PRAGMA hard_heap_limit=67108864; PRAGMA trusted_schema=OFF; PRAGMA journal_mode=OFF; PRAGMA temp_store=MEMORY;
 PRAGMA mmap_size=0; PRAGMA cache_size=-2048; PRAGMA max_page_count=65536`,
	)
	if err != nil {
		return fmt.Errorf("configure restore worker: %w", err)
	}

	service := NewSettingsService(database)

	return service.restoreValidatedFile(ctx, restoreReadOnlyURI(source))
}

func restoreReadOnlyURI(path string) string {
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uri.Path, "/") {
		uri.Path = "/" + uri.Path
	}

	options := uri.Query()
	options.Set("mode", "ro")
	options.Set("immutable", "1")
	uri.RawQuery = options.Encode()

	return uri.String()
}

func prepareRestore(ctx context.Context, source string) (string, func(), error) {
	select {
	case restoreWorkerSlot <- struct{}{}:
	case <-ctx.Done():
		return "", nil, fmt.Errorf("wait for restore worker: %w", ctx.Err())
	}

	defer func() { <-restoreWorkerSlot }()

	dir, err := os.MkdirTemp("", "portfolio-restore-worker-")
	if err != nil {
		return "", nil, fmt.Errorf("create restore workspace: %w", err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }

	valid := false
	defer func() {
		if !valid {
			cleanup()
		}
	}()

	input, output := filepath.Join(dir, "input.db"), filepath.Join(dir, "clean.db")

	err = stageRestore(ctx, source, input)
	if err != nil {
		return "", nil, err
	}

	executable, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("locate restore worker: %w", err)
	}

	command := exec.CommandContext(
		ctx,
		executable,
		restoreWorkerArgument,
		input,
		output,
	)
	command.Dir = dir
	command.Env = []string{
		"GOMAXPROCS=1",
		"GOMEMLIMIT=128MiB",
		"TMPDIR=" + dir,
		"TEMP=" + dir,
		"TMP=" + dir,
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
	}
	command.WaitDelay = time.Second
	// Discard child diagnostics: malformed backups must not put private contents
	// or unbounded SQLite/runtime errors into server logs or responses.
	err = command.Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("restore worker: %w", ctx.Err())
		}

		return "", nil, fmt.Errorf(
			"%w: restore worker rejected backup or exceeded resource budget",
			ErrIncompatibleBackup,
		)
	}

	valid = true

	return output, cleanup, nil
}

func stageRestore(ctx context.Context, source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("%w: open backup: %w", ErrIncompatibleBackup, err)
	}
	defer func() { _ = input.Close() }()

	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("inspect backup: %w", err)
	}

	if !info.Mode().IsRegular() || info.Size() > restoreMaxFileBytes {
		return fmt.Errorf("%w: backup file limit", ErrIncompatibleBackup)
	}

	const privateFileMode = 0o600

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFileMode)
	if err != nil {
		return fmt.Errorf("create private backup: %w", err)
	}

	defer func() { _ = output.Close() }()

	const copyBufferBytes = 32 << 10

	buffer := make([]byte, copyBufferBytes)

	var copied int

	for {
		err = ctx.Err()
		if err != nil {
			return fmt.Errorf("stage backup: %w", err)
		}

		count, readErr := input.Read(buffer)

		copied += count
		if copied > restoreMaxFileBytes {
			return fmt.Errorf("%w: backup file limit", ErrIncompatibleBackup)
		}

		_, err = output.Write(buffer[:count])
		if err != nil {
			return fmt.Errorf("write private backup: %w", err)
		}

		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return fmt.Errorf("read backup: %w", readErr)
		}
	}

	err = output.Close()
	if err != nil {
		return fmt.Errorf("close private backup: %w", err)
	}

	return nil
}
