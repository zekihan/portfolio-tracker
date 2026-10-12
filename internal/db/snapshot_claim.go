package db

import (
	"context"
	"errors"
)

// ErrSnapshotSuperseded means clear/restore or a newer scheduler owner invalidated
// a valuation while it was being fetched. The caller may retry with fresh data.
var ErrSnapshotSuperseded = errors.New("snapshot superseded by a data replacement or newer lease")

type snapshotClaimKey struct{}

type snapshotClaim struct {
	owner string
	slot  int64
}

// WithSnapshotClaim binds scheduled persistence to an owner and UTC hourly slot.
// Manual snapshots carry no claim and remain independent of the hourly schedule.
func WithSnapshotClaim(ctx context.Context, slot int64, owner string) context.Context {
	return context.WithValue(ctx, snapshotClaimKey{}, snapshotClaim{slot: slot, owner: owner})
}

// SnapshotClaim returns the scheduled claim, or an empty owner for manual work.
func SnapshotClaim(ctx context.Context) (int64, string) {
	claim, ok := ctx.Value(snapshotClaimKey{}).(snapshotClaim)
	if !ok {
		return 0, ""
	}

	return claim.slot, claim.owner
}
