package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zekihan/portfolio-tracker/internal/db"
	"github.com/zekihan/portfolio-tracker/internal/services"
)

func TestHistoryPageValidatesDayRange(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	_, err = database.Exec(`INSERT INTO snapshots
		(timestamp, total_value_try, total_value_usd, usd_try_rate, holdings_json)
		VALUES (datetime('now', '-2 days'), 400, 10, 40, '[]')`)
	if err != nil {
		t.Fatal(err)
	}

	handler := New(services.NewHoldingService(database, nil),
		services.NewSnapshotService(database, nil, nil), nil, nil)
	router := gin.New()
	router.SetHTMLTemplate(
		template.Must(template.New("history.html").Parse(`{{.Days}}/{{len .ChartData}}`)),
	)
	router.GET("/history", handler.pageHistory)

	for _, test := range []struct {
		query string
		body  string
	}{
		{query: "?days=-1", body: "365/1"},
		{query: "?days=0", body: "365/1"},
		{query: "?days=36501", body: "365/1"},
		{query: "?days=9223372036854775807", body: "365/1"},
		{query: "?days=abc", body: "365/1"},
		{query: "", body: "365/1"},
		{query: "?days=1", body: "1/0"},
		{query: "?days=36500", body: "36500/1"},
	} {
		t.Run(test.query, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/history"+test.query, http.NoBody)
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != test.body {
				t.Errorf(
					"response = %d %q, want 200 %q",
					response.Code,
					response.Body.String(),
					test.body,
				)
			}
		})
	}
}
