package handlers

import (
	"testing"
	"time"
)

func TestFormatTimeUsesUTC(t *testing.T) {
	t.Parallel()

	instant := time.Date(2026, time.October, 11, 12, 34, 0, 0, time.UTC)

	zones := []*time.Location{
		time.UTC,
		time.FixedZone("UTC+3", 3*60*60),
		time.FixedZone("UTC-5", -5*60*60),
	}
	for _, zone := range zones {
		t.Run(zone.String(), func(t *testing.T) {
			t.Parallel()

			got := formatTime(instant.In(zone))

			want := "2026-10-11 12:34 UTC"
			if got != want {
				t.Errorf("formatTime = %q, want %q", got, want)
			}
		})
	}
}
