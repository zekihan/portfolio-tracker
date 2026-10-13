package services

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

//nolint:gocyclo // Legacy snapshots require independent validation of values and optional quality flags.
func decodeHoldingHistoryPoint(
	timestamp time.Time,
	holding map[string]json.RawMessage,
) HoldingHistoryPoint {
	point := HoldingHistoryPoint{Timestamp: timestamp}

	var (
		valueTRY, valueUSD *float64
		valueEUR           *float64
	)

	eurErr := json.Unmarshal(holding["value_eur"], &valueEUR)
	if eurErr == nil && valueEUR != nil && areFiniteValues(*valueEUR) && *valueEUR >= 0 {
		point.ValueEUR = valueEUR
	}

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

// findHoldingHistoryPoint decodes one entry at a time and observes caller cancellation.
func findHoldingHistoryPoint(
	ctx context.Context,
	timestamp time.Time,
	raw string,
	holdingID int,
) (HoldingHistoryPoint, bool, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))

	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return HoldingHistoryPoint{}, false, wrapOperationError("decode holding history", ctx.Err())
	}

	var point HoldingHistoryPoint

	found := false

	for decoder.More() {
		cancelErr := ctx.Err()
		if cancelErr != nil {
			return HoldingHistoryPoint{}, false, wrapOperationError(
				"decode holding history",
				cancelErr,
			)
		}

		var holding map[string]json.RawMessage

		err = decoder.Decode(&holding)
		if err != nil {
			return HoldingHistoryPoint{}, false, wrapOperationError(
				"decode holding history",
				ctx.Err(),
			)
		}

		var id int

		err = json.Unmarshal(holding["id"], &id)
		if err == nil && id == holdingID && !found {
			point = decodeHoldingHistoryPoint(timestamp, holding)
			found = true
		}
	}
	// Preserve the previous behavior of skipping malformed snapshots in their entirety.
	_, err = decoder.Token()
	if err != nil {
		return HoldingHistoryPoint{}, false, wrapOperationError("decode holding history", ctx.Err())
	}

	err = restoreJSONEnd(decoder)
	if err != nil {
		return HoldingHistoryPoint{}, false, wrapOperationError("decode holding history", ctx.Err())
	}

	return point, found, wrapOperationError("decode holding history", ctx.Err())
}
