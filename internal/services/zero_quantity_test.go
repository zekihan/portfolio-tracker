package services

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
)

//nolint:gocyclo // Exercise row, aggregate, history, and positive-quantity control in one portfolio.
func TestZeroQuantityMissingQuoteKeepsPortfolioComplete(t *testing.T) {
	t.Parallel()
	database := valuationDatabase(t)

	_, err := database.ExecContext(t.Context(), `INSERT INTO holdings
 (name, asset_type, quantity, price_provider, base_currency, manual_price)
 VALUES ('Cash', 'Cash', 100, 'Manual', 'USD', 1),
 ('Closed', 'Stock', 0, 'Manual', 'USD', 0)`)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(database, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	holdings := NewHoldingService(database, prices)

	rows, err := holdings.GetWithPrices(t.Context(), "USD")
	if err != nil {
		t.Fatal(err)
	}

	var closed models.HoldingWithPrice

	for _, row := range rows {
		if row.Name == "Closed" {
			closed = row
		}
	}

	if closed.ID == 0 || closed.ValuationAvailable || !closed.IsFailed || closed.PriceError == "" ||
		closed.CurrentPrice != 0 ||
		closed.PriceSource != "" ||
		closed.CurrentCurrency != "" {
		t.Fatalf("missing quote was hidden or fabricated: %+v", closed)
	}

	data, err := holdings.GetDashboardData(t.Context(), "USD")
	if err != nil {
		t.Fatal(err)
	}

	if !data.ValuationComplete || data.ValuedCount != 2 || data.UnvaluedCount != 0 ||
		data.FailedCount != 1 ||
		data.CurrentValueUSD != 100 ||
		data.CurrentValueTRY != 4000 {
		t.Fatalf("zero holding changed aggregate completeness: %+v", data)
	}

	snapshots := NewSnapshotService(database, prices, holdings)

	err = snapshots.CreateSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	assertZeroQuantitySnapshot(t, snapshots, closed.ID)

	quantity := 1.0

	_, err = holdings.Patch(t.Context(), closed.ID, &models.HoldingPatch{Quantity: &quantity})
	if err != nil {
		t.Fatal(err)
	}

	data, err = holdings.GetDashboardData(t.Context(), "USD")
	if err != nil {
		t.Fatal(err)
	}

	if data.ValuationComplete || data.ValuedCount != 1 || data.UnvaluedCount != 1 {
		t.Fatalf("positive unquoted holding considered complete: %+v", data)
	}

	var incomplete *IncompleteValuationError

	err = snapshots.CreateSnapshot(t.Context())
	if !errors.As(err, &incomplete) ||
		incomplete.UnvaluedCount != 1 {
		t.Fatalf("positive unquoted holding allowed snapshot: %v", err)
	}
}

//nolint:gocyclo // Verify the same snapshot across serialization, restore, history, and chart readers.
func assertZeroQuantitySnapshot(t *testing.T, snapshots *SnapshotService, holdingID int) {
	t.Helper()

	saved, err := snapshots.GetSnapshots(t.Context(), 10, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(saved) != 1 || saved[0].TotalValueUSD != 100 || saved[0].TotalValueTRY != 4000 ||
		saved[0].FailedCount != 0 {
		t.Fatalf("snapshot totals or quality changed: %+v", saved)
	}

	_, err = validateRestoreSnapshotJSON(t.Context(), saved[0].HoldingsJSON)
	if err != nil {
		t.Fatalf("snapshot cannot be restored: %v", err)
	}

	var rows []map[string]json.RawMessage

	err = json.Unmarshal([]byte(saved[0].HoldingsJSON), &rows)
	if err != nil {
		t.Fatal(err)
	}

	for _, row := range rows {
		var id int

		err = json.Unmarshal(row["id"], &id)
		if err != nil {
			t.Fatal(err)
		}

		if id == holdingID {
			if _, present := row["price"]; present {
				t.Fatal("snapshot fabricated a missing unit quote")
			}

			if _, present := row["currency"]; present {
				t.Fatal("snapshot fabricated a missing quote currency")
			}
		}
	}

	points, err := snapshots.GetHoldingHistory(t.Context(), holdingID, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(points) != 1 || !points[0].ValuationAvailable || !points[0].IsFailed ||
		points[0].ValueUSD != 0 ||
		points[0].ValueTRY != 0 {
		t.Fatalf("known zero history or missing quote quality lost: %+v", points)
	}

	chart, err := snapshots.GetSnapshotsForChart(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(chart) != 1 || chart[0].ValueUSD != 100 || chart[0].FailedCount != 0 {
		t.Fatalf("portfolio chart lost total or quality: %+v", chart)
	}
}
