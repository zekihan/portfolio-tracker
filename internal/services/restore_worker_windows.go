package services

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func limitRestoreWorker() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create restore job: %w", err)
	}
	// Keep the handle alive for the child lifetime. Closing a kill-on-close job
	// from inside the worker would terminate it before validation can complete.
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION

	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
		windows.JOB_OBJECT_LIMIT_PROCESS_TIME | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS

	const ticksPerSecond = 10000000

	limits.BasicLimitInformation.PerProcessUserTimeLimit = restoreWorkerCPUSeconds * ticksPerSecond
	limits.BasicLimitInformation.ActiveProcessLimit = 1
	limits.ProcessMemoryLimit = restoreWorkerMemoryBytes

	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	)
	if err != nil {
		closeErr := windows.CloseHandle(job)

		return fmt.Errorf("limit restore job: %w", errors.Join(err, closeErr))
	}

	err = windows.AssignProcessToJobObject(job, windows.CurrentProcess())
	if err != nil {
		closeErr := windows.CloseHandle(job)

		return fmt.Errorf("assign restore job: %w", errors.Join(err, closeErr))
	}

	return nil
}
