//go:build linux || darwin

package services

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type restoreResourceLimit struct {
	resource int
	value    uint64
}

func limitRestoreWorker() error {
	limits, err := restoreWorkerMemoryLimits()
	if err != nil {
		return err
	}

	limits = append(limits,
		restoreResourceLimit{unix.RLIMIT_CPU, restoreWorkerCPUSeconds},
		restoreResourceLimit{unix.RLIMIT_FSIZE, restoreMaxFileBytes},
		restoreResourceLimit{unix.RLIMIT_CORE, 0},
	)

	for _, limit := range limits {
		err = unix.Setrlimit(limit.resource, &unix.Rlimit{Cur: limit.value, Max: limit.value})
		if err != nil {
			return fmt.Errorf("set restore resource limit %d: %w", limit.resource, err)
		}
	}

	return nil
}

// Measure startup mappings before opening any uploaded database. In particular,
// the race runtime reserves a very large shadow arena. Bound additional data
// allocations and virtual mappings without invalidating those existing arenas.
func restoreWorkerMemoryLimits() ([]restoreResourceLimit, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}

	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return nil, fmt.Errorf("read worker startup memory: %w", err)
	}

	fields := strings.Fields(string(raw))

	const dataField = 5
	if len(fields) <= dataField {
		return nil, fmt.Errorf("%w: invalid worker memory accounting", ErrIncompatibleBackup)
	}

	addressPages, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("read worker address space: %w", err)
	}

	dataPages, err := strconv.ParseUint(fields[dataField], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("read worker data memory: %w", err)
	}

	const additionalAddressBytes = 2 << 30

	pageSize := os.Getpagesize()
	if pageSize <= 0 {
		return nil, fmt.Errorf("%w: invalid worker page size", ErrIncompatibleBackup)
	}

	pageBytes := uint64(pageSize)

	return []restoreResourceLimit{
		{unix.RLIMIT_DATA, dataPages*pageBytes + restoreWorkerMemoryBytes},
		{unix.RLIMIT_AS, addressPages*pageBytes + additionalAddressBytes},
	}, nil
}
