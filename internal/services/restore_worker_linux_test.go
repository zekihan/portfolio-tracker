package services

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRestoreWorkerHardLimits(t *testing.T) {
	for _, mode := range []string{"memory", "cpu", "file", "limits", "wall"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 9*time.Second)
			defer cancel()

			if mode == "wall" {
				cancel()

				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
			}

			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}

			command := exec.CommandContext(
				ctx,
				executable,
				"-test.run=^TestRestoreWorkerResourceHelper$",
			)
			command.Env = []string{
				"OPS89_RESOURCE_HELPER=" + mode,
				"GOMAXPROCS=1",
				"TMPDIR=" + t.TempDir(),
			}
			command.Stdout, command.Stderr = io.Discard, io.Discard
			err = command.Run()

			switch mode {
			case "file", "limits":
				if err != nil {
					t.Fatalf("worker resource limit check failed: %v", err)
				}
			case "wall":
				if err == nil || ctx.Err() == nil {
					t.Fatalf("wall clock limit not enforced: %v", err)
				}
			default:
				if err == nil || ctx.Err() != nil {
					t.Fatalf(
						"worker survived %s limit, or hit wall timeout: %v, %v",
						mode,
						err,
						ctx.Err(),
					)
				}
			}
		})
	}
}

func TestRestoreWorkerResourceHelper(t *testing.T) {
	mode := os.Getenv("OPS89_RESOURCE_HELPER")
	if mode == "" {
		t.Skip("subprocess helper")
	}

	err := limitRestoreWorker()
	if err != nil {
		t.Fatal(err)
	}

	switch mode {
	case "limits":
		checkRestoreWorkerLimits(t)
	case "memory":
		memory := make([]byte, restoreWorkerMemoryBytes*2)
		for index := range memory {
			memory[index] = 1
		}

		runtime.KeepAlive(memory)
	case "cpu":
		for {
			runtime.Gosched()
		}
	case "wall":
		<-time.NewTimer(time.Minute).C
	case "file":
		file, err := os.Create(filepath.Join(t.TempDir(), "budget.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()

		err = file.Truncate(restoreMaxFileBytes + 1)
		if err == nil {
			t.Fatal("worker exceeded file budget")
		}
	default:
		t.Fatal("unknown helper mode")
	}
}

func checkRestoreWorkerLimits(t *testing.T) {
	t.Helper()

	for resource, want := range map[int]uint64{
		unix.RLIMIT_DATA:  restoreWorkerMemoryBytes,
		unix.RLIMIT_CPU:   restoreWorkerCPUSeconds,
		unix.RLIMIT_FSIZE: restoreMaxFileBytes,
	} {
		var limit unix.Rlimit

		err := unix.Getrlimit(resource, &limit)
		if err != nil || limit.Cur != limit.Max || limit.Cur == unix.RLIM_INFINITY ||
			limit.Cur < want ||
			(resource != unix.RLIMIT_DATA && limit.Cur != want) {
			t.Fatalf("limit %d = %+v, %v", resource, limit, err)
		}
	}
}
