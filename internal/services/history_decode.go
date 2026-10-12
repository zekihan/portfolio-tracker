package services

import (
	"encoding/json"
	"time"
)

//nolint:gocyclo // Legacy snapshots require independent validation of values and optional quality flags.
func decodeHoldingHistoryPoint(
	timestamp time.Time,
	holding map[string]json.RawMessage,
) HoldingHistoryPoint {
	point := HoldingHistoryPoint{Timestamp: timestamp}

	var valueTRY, valueUSD *float64

	tryErr := json.Unmarshal(holding["value_try"], &valueTRY)
	usdErr := json.Unmarshal(holding["value_usd"], &valueUSD)

	var stale, failed bool
	if raw, exists := holding["is_stale"]; exists {
		err := json.Unmarshal(raw, &stale)
		if err != nil {
			failed = true
		}
	}

	if raw, exists := holding["is_failed"]; exists {
		var flag bool

		err := json.Unmarshal(raw, &flag)
		if err != nil {
			failed = true
		} else {
			failed = failed || flag
		}
	}

	point.IsStale, point.IsFailed = stale, failed

	available := !failed
	if raw, exists := holding["valuation_available"]; exists {
		err := json.Unmarshal(raw, &available)
		if err != nil {
			available = false
		}
	}

	point.ValuationAvailable = available && tryErr == nil && usdErr == nil && valueTRY != nil &&
		valueUSD != nil
	if valueTRY != nil {
		point.ValueTRY = *valueTRY
	}

	if valueUSD != nil {
		point.ValueUSD = *valueUSD
	}

	point.ValuationAvailable = point.ValuationAvailable &&
		areFiniteValues(point.ValueTRY, point.ValueUSD)

	return point
}
