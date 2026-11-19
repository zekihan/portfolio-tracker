package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/models"
	"github.com/zekihan/portfolio-tracker/internal/price"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestHoldingDialogs(t *testing.T) {
	t.Chdir("../..")

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	prices := price.NewService(database, price.DefaultRegistry())
	holdings := services.NewHoldingService(database, prices)

	var holding models.Holding

	holding.Name = "Example <script>"
	holding.AssetType = models.AssetTypeStock
	holding.BaseCurrency = "USD"
	holding.PriceProvider = "Yahoo Finance"
	holding.Notes = "\n\nFirst line\n<example> & second line"

	holding.Quantity = 0.000001

	err = holdings.Create(t.Context(), &holding)
	if err != nil {
		t.Fatal(err)
	}

	router := New(holdings, nil, nil, prices).SetupRoutes()

	for _, test := range []struct {
		path   string
		marker string
		status int
	}{
		{"/partials/holding-form", `data-id="0"`, http.StatusOK},
		{"/partials/holding-form/1", `value="1e-06"`, http.StatusOK},
		{"/partials/holding-duplicate/1", `Example &lt;script&gt; (copy)`, http.StatusOK},
		{"/partials/manual-price/1", `Price (USD)`, http.StatusOK},
		{"/partials/holding-form/999", "", http.StatusNotFound},
		{"/partials/manual-price/999", "", http.StatusNotFound},
		{"/partials/holding-form/wrong", "", http.StatusBadRequest},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil),
			)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}

			body := response.Body.String()
			if !strings.Contains(body, test.marker) {
				t.Errorf("missing %q in %s", test.marker, body)
			}

			if strings.Contains(body, "<script>") {
				t.Error("holding name rendered as executable HTML")
			}

			if test.status == http.StatusOK && !strings.Contains(body, "</form>") {
				t.Error("incomplete dialog")
			}

			if test.path == "/partials/holding-form/1" ||
				test.path == "/partials/holding-duplicate/1" {
				checkHoldingDialogNotes(t, body, holding.Notes)
			}
		})
	}
}

func checkHoldingDialogNotes(t *testing.T, body, want string) {
	t.Helper()

	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	for node := range root.Descendants() {
		if node.DataAtom == atom.Textarea {
			if node.FirstChild == nil {
				t.Fatal("holding notes textarea is empty")
			}

			if got := node.FirstChild.Data; got != want {
				t.Errorf("holding notes textarea = %q, want %q", got, want)
			}

			return
		}
	}

	t.Fatal("holding notes textarea is missing")
}
