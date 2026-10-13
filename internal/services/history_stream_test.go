package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFindHoldingHistoryPointValidatesEntireSnapshot(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`[{"id":1,"value_try":40,"value_usd":1},`,
		`[{"id":1,"value_try":40,"value_usd":1}] []`,
		`[{"id":1,"value_try":40,"value_usd":1},bad]`,
	} {
		_, found, err := findHoldingHistoryPoint(t.Context(), time.Now(), raw, 1)
		if err != nil || found {
			t.Fatalf("malformed snapshot accepted: found=%v, err=%v", found, err)
		}
	}
}

func TestFindHoldingHistoryPointObservesCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := findHoldingHistoryPoint(
		ctx,
		time.Now(),
		`[{"id":1,"value_try":40,"value_usd":1}]`,
		1,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func BenchmarkHoldingHistoryStreaming(b *testing.B) {
	raw := `[` + strings.Repeat(
		`{"id":2,"value_try":40,"value_usd":1},`,
		10000,
	) + `{"id":1,"value_try":40,"value_usd":1}]`

	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))

	for b.Loop() {
		_, found, err := findHoldingHistoryPoint(b.Context(), time.Time{}, raw, 1)
		if err != nil || !found {
			b.Fatalf("found=%v, err=%v", found, err)
		}
	}
}
