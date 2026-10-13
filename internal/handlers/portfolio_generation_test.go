package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

type generationFixture struct {
	router   http.Handler
	database *sqlx.DB
	holdings *services.HoldingService
	backup   []byte
}

func newGenerationFixture(t *testing.T) generationFixture {
	t.Helper()

	live, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = live.Close() })

	source, err := db.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = source.Close() })

	holdings := services.NewHoldingService(live, nil)
	for _, seed := range []struct {
		database *sqlx.DB
		name     string
	}{{live, "Live A"}, {source, "Restored B"}} {
		holding := models.Holding{
			Name:         seed.name,
			AssetType:    models.AssetTypeCash,
			BaseCurrency: "TRY",
			Quantity:     10,
		}

		err = services.NewHoldingService(seed.database, nil).
			Create(t.Context(), &holding)
		if err != nil {
			t.Fatal(err)
		}

		if holding.ID != 1 {
			t.Fatal("independent fixture should have ID 1")
		}

		_, err = seed.database.ExecContext(
			t.Context(),
			`INSERT INTO snapshots (total_value_try, total_value_usd, usd_try_rate, holdings_json) VALUES (10, 1, 10, '[]')`,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "backup.db")

	err = services.NewSettingsService(source).Backup(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	backup, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	prices := price.NewService(live, price.DefaultRegistry())

	t.Cleanup(func() { _ = prices.Close() })

	router := New(
		holdings,
		services.NewSnapshotService(live, prices, holdings),
		services.NewSettingsService(live),
		prices,
	).SetupRoutes()

	return generationFixture{router: router, database: live, holdings: holdings, backup: backup}
}

func restoreGenerationFixture(t *testing.T, fixture generationFixture) {
	t.Helper()
	request := restoreRequest(t, "backup.db", fixture.backup, true)
	request.Header.Set(portfolioGenerationHeader, "0")

	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", response.Code, response.Body.String())
	}
}

func staleHoldingBody(t *testing.T, fixture generationFixture) []byte {
	t.Helper()

	read := httptest.NewRecorder()
	fixture.router.ServeHTTP(
		read,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/holdings/1", nil),
	)

	if read.Code != http.StatusOK || read.Header().Get(portfolioGenerationHeader) != "0" {
		t.Fatalf("initial read: %d %v", read.Code, read.Header())
	}

	var old models.Holding

	err := json.Unmarshal(read.Body.Bytes(), &old)
	if err != nil {
		t.Fatal(err)
	}

	old.Quantity = 12

	body, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}

	return body
}

func TestStaleHoldingActionsAfterRestore(t *testing.T) {
	t.Chdir("../..")

	for _, method := range []string{http.MethodDelete, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			fixture := newGenerationFixture(t)
			body := staleHoldingBody(t, fixture)

			restoreGenerationFixture(t, fixture)

			for _, generation := range []string{"0", "1"} {
				var payload io.Reader
				if method == http.MethodPut {
					payload = bytes.NewReader(body)
				}

				request := httptest.NewRequestWithContext(
					t.Context(),
					method,
					"/api/holdings/1",
					payload,
				)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set(portfolioGenerationHeader, generation)

				response := httptest.NewRecorder()
				fixture.router.ServeHTTP(response, request)

				want := http.StatusConflict
				if generation == "1" {
					want = http.StatusOK
				}

				if response.Code != want {
					t.Fatalf(
						"generation %s: %d %s",
						generation,
						response.Code,
						response.Body.String(),
					)
				}

				if generation == "0" {
					stored, readErr := fixture.holdings.GetByID(t.Context(), 1)
					if readErr != nil || stored == nil || stored.Name != "Restored B" ||
						stored.Quantity != 10 {
						t.Fatalf("stale action changed restored holding: %+v %v", stored, readErr)
					}
				}
			}
		})
	}
}

func TestMutationGenerationRequiredAndAtomic(t *testing.T) {
	t.Chdir("../..")

	mutations := []struct {
		method, path, body string
		status             int
	}{
		{
			http.MethodPost,
			"/api/holdings",
			`{"name":"New","asset_type":"Cash","base_currency":"TRY","quantity":1}`,
			http.StatusCreated,
		},
		{http.MethodPatch, "/api/holdings/1", `{"quantity":12}`, http.StatusNoContent},
		{http.MethodPatch, "/api/holdings/1/est-price", `{"estPrice":12}`, http.StatusOK},
		{http.MethodPatch, "/api/holdings/1/manual-price", `{"price":12}`, http.StatusOK},
		{http.MethodDelete, "/api/holdings/1/manual-price", "", http.StatusOK},
		{
			http.MethodPut,
			"/api/holdings/1/provider",
			`{"provider":"Manual","instrumentId":""}`,
			http.StatusOK,
		},
		{http.MethodDelete, "/api/history/snapshots/1", "", http.StatusNoContent},
		{http.MethodDelete, "/api/history/snapshots", "", http.StatusNoContent},
		{http.MethodPatch, "/api/settings/currency", `{"currency":"USD"}`, http.StatusOK},
		{http.MethodPatch, "/api/settings/dark-mode", `{"mode":"dark"}`, http.StatusOK},
		{http.MethodPatch, "/api/settings/refresh-interval", `{"interval":100}`, http.StatusOK},
		{http.MethodDelete, "/api/settings/clear-all", "", http.StatusOK},
		{http.MethodPost, "/api/settings/restore", "", http.StatusOK},
	}
	for _, mutation := range mutations {
		t.Run(mutation.method+mutation.path, func(t *testing.T) {
			fixture := newGenerationFixture(t)
			restoreGenerationFixture(t, fixture)

			for _, token := range []struct {
				value  string
				status int
			}{
				{"", http.StatusPreconditionRequired},
				{"-1", http.StatusBadRequest},
				{"0", http.StatusConflict},
				{"1", mutation.status},
			} {
				request := httptest.NewRequestWithContext(
					t.Context(),
					mutation.method,
					mutation.path,
					strings.NewReader(mutation.body),
				)
				request.Header.Set("Content-Type", "application/json")

				if mutation.path == "/api/settings/restore" {
					request = restoreRequest(t, "backup.db", fixture.backup, true)
				}

				if token.value != "" {
					request.Header.Set(portfolioGenerationHeader, token.value)
				}

				request.Header.Set("X-Clear-History-Confirm", "clear-history")

				response := httptest.NewRecorder()
				fixture.router.ServeHTTP(response, request)

				if response.Code != token.status {
					t.Fatalf(
						"token %q: status %d want %d: %s",
						token.value,
						response.Code,
						token.status,
						response.Body.String(),
					)
				}
			}
		})
	}
}

type delayedMutationBody struct {
	reader  *strings.Reader
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (body *delayedMutationBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() { close(body.entered); <-body.resume })

	//nolint:wrapcheck // Preserve io.Reader EOF semantics.
	return body.reader.Read(buffer)
}

func TestRestoreBetweenHeaderAndMutationRejectsOldWrite(t *testing.T) {
	t.Chdir("../..")
	fixture := newGenerationFixture(t)
	body := &delayedMutationBody{
		reader:  strings.NewReader(`{"quantity":999}`),
		entered: make(chan struct{}),
		resume:  make(chan struct{}),
	}
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/api/holdings/1",
		body,
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(portfolioGenerationHeader, "0")

	response := httptest.NewRecorder()

	done := make(chan struct{})
	go func() { defer close(done); fixture.router.ServeHTTP(response, request) }()

	<-body.entered // Header was accepted, but the SQL mutation has not run.
	restoreGenerationFixture(t, fixture)
	close(body.resume)
	<-done

	if response.Code != http.StatusConflict {
		t.Fatalf("racing write: %d %s", response.Code, response.Body.String())
	}

	stored, err := fixture.holdings.GetByID(t.Context(), 1)
	if err != nil || stored.Name != "Restored B" || stored.Quantity != 10 {
		t.Fatalf("racing write changed backup: %+v %v", stored, err)
	}
}

func TestReadGenerationIsCapturedBeforeData(t *testing.T) {
	t.Chdir("../..")
	fixture := newGenerationFixture(t)
	handler := New(fixture.holdings, nil, nil, nil)
	endpoint := handler.capturePortfolioGeneration(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := fixture.database.ExecContext(
				r.Context(),
				"UPDATE snapshot_state SET generation = generation + 1 WHERE id = 1",
			)
			if err != nil {
				t.Fatal(err)
			}

			generation, ok := db.PortfolioGeneration(r.Context())
			if !ok || generation != 0 {
				t.Fatalf("read token adopted newer data: %d %t", generation, ok)
			}

			w.WriteHeader(http.StatusOK)
		}),
	)
	response := httptest.NewRecorder()
	endpoint.ServeHTTP(
		response,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/holdings", nil),
	)

	if response.Header().Get(portfolioGenerationHeader) != "0" {
		t.Fatalf("read exposed newer token: %v", response.Header())
	}
}
