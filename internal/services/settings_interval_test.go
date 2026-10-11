package services

import (
	"path/filepath"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

func TestSettingsRefreshIntervalValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  int
	}{
		{value: "", want: 60},
		{value: "invalid", want: 60},
		{value: "60seconds", want: 60},
		{value: "120seconds", want: 60},
		{value: "0", want: 60},
		{value: "-1", want: 60},
		{value: "9", want: 60},
		{value: "3601", want: 60},
		{value: "999999999999999999999999999999", want: 60},
		{value: "10", want: 10},
		{value: "120", want: 120},
		{value: "3600", want: 3600},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()

			database, err := db.Open(filepath.Join(t.TempDir(), "portfolio.db"))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = database.Close() })

			err = db.SetSetting(database, "refresh_interval", test.value)
			if err != nil {
				t.Fatal(err)
			}

			service := NewSettingsService(database)

			settings, err := service.GetAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if settings.RefreshInterval != test.want {
				t.Errorf("refresh interval = %d, want %d", settings.RefreshInterval, test.want)
			}
		})
	}
}
