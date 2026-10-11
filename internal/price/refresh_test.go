package price

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestRefreshAllReportsAllFailures(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewService(database, NewRegistry())

	var first models.Holding

	first.PriceProvider = "Missing A"
	first.InstrumentID = "FIRST"
	second := first
	second.PriceProvider = "Missing B"
	second.InstrumentID = "SECOND"

	err = service.RefreshAll(t.Context(), []models.Holding{first, second})
	if err == nil {
		t.Fatal("refresh reported success despite failed price lookups")
	}

	if !strings.Contains(err.Error(), first.PriceProvider) ||
		!strings.Contains(err.Error(), second.PriceProvider) {
		t.Fatalf("refresh did not report both failures: %v", err)
	}
}

func TestRefreshAllSkipsUnconfiguredHoldings(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	service := NewService(database, NewRegistry())

	var manual, missingProvider, missingInstrument models.Holding

	manual.PriceProvider = "Manual"
	manual.InstrumentID = "TEST"
	missingProvider.InstrumentID = "TEST"
	missingInstrument.PriceProvider = "Unknown"

	err = service.RefreshAll(t.Context(),
		[]models.Holding{manual, missingProvider, missingInstrument})
	if err != nil {
		t.Fatalf("unconfigured holdings should be skipped: %v", err)
	}
}
