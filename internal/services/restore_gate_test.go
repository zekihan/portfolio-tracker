package services

import (
	"context"
	"sync"
)

// Keep test restores from consuming their production deadline while queued.
var restoreTestSlot sync.Mutex

func (service *SettingsService) restoreForTest(ctx context.Context, path string) error {
	restoreTestSlot.Lock()
	defer restoreTestSlot.Unlock()

	return service.RestoreFrom(ctx, path)
}
