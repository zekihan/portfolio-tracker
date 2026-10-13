package price

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestProviderMetadataObservesExternalResetGeneration(t *testing.T) {
	for _, scenario := range []string{"reset", "no-op", "failed"} {
		t.Run(scenario, func(t *testing.T) { checkExternalMetadataReset(t, scenario) })
	}
}

func checkExternalMetadataReset(t *testing.T, scenario string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "portfolio.db")

	firstDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = firstDB.Close() })

	secondDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = secondDB.Close() })

	var revision, calls atomic.Int32
	revision.Store(1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprintf(
			w,
			`{"items":[{"abbreviation":"AAA","fundPrice":"%d"},{"abbreviation":"BBB","fundPrice":"%d"}]}`,
			revision.Load()*10,
			revision.Load()*10,
		)
	}))
	t.Cleanup(server.Close)

	getter := NewPusulaPortfoy()
	setRegressionTransport(t, getter.client, server.URL)

	registry := NewRegistry()
	registry.Register(getter)
	first := NewService(firstDB, registry)
	second := NewService(secondDB, NewRegistry())

	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })

	_, err = first.GetPrice(t.Context(), getter.Name(), "AAA")
	if err != nil {
		t.Fatal(err)
	}

	revision.Store(2)

	err = second.WithCacheReset(t.Context(), func() error {
		if scenario == "no-op" {
			return nil
		}

		tx, beginErr := secondDB.BeginTxx(t.Context(), nil)
		if beginErr != nil {
			return fmt.Errorf("begin reset: %w", beginErr)
		}

		defer func() { _ = tx.Rollback() }()

		_, resetErr := tx.ExecContext(
			t.Context(),
			"UPDATE snapshot_state SET generation=generation+1 WHERE id=1; DELETE FROM price_cache",
		)
		if resetErr != nil {
			return fmt.Errorf("reset cache: %w", resetErr)
		}

		if scenario == "failed" {
			return errProviderUnavailable
		}

		return wrapOperationError("commit reset", tx.Commit())
	})
	if (err != nil) != (scenario == "failed") {
		t.Fatalf("reset error=%v", err)
	}

	quote, err := first.GetPrice(t.Context(), getter.Name(), "BBB")
	if err != nil {
		t.Fatal(err)
	}

	wantValue, wantCalls := 10.0, int32(1)
	if scenario == "reset" {
		wantValue, wantCalls = 20, 2
	}

	if quote.Value != wantValue || calls.Load() != wantCalls {
		t.Fatalf("quote=%v calls=%d, want %v/%d", quote.Value, calls.Load(), wantValue, wantCalls)
	}
}
