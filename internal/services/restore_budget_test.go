package services

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRestoreRejectsJSONResourceAmplification(t *testing.T) {
	t.Parallel()

	fixtures := map[string]string{
		"row bytes": strings.Repeat(" ", restoreMaxJSONBytes+1),
		"object bytes": `[{"id":1,` + strings.Repeat(
			" ",
			restoreMaxObjectBytes,
		) + `"quantity":1}]`,
		"string bytes": `[{"id":1,"name":"` + strings.Repeat(
			"x",
			restoreMaxStringBytes+1,
		) + `"}]`,
		"escaped string bytes": `[{"id":1,"name":"` + strings.Repeat(
			`\u0061`,
			restoreMaxStringBytes,
		) + `"}]`,
		"fields": `[{"id":1,` + strings.Repeat(
			`"quantity":1,`,
			restoreMaxFields,
		) + `"price":1}]`,
		"unknown field":   `[{"id":1,"unknown":"private"}]`,
		"case alias":      `[{"id":55,"ID":1}]`,
		"duplicate field": `[{"id":1,"id":2}]`,
		"nested object":   `[{"id":1,"name":{"a":1}}]`,
		"nested array":    `[{"id":1,"name":[1]}]`,
		"elements":        `[` + strings.Repeat(`{"id":1},`, restoreMaxHoldings) + `{"id":2}]`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := validateRestoreSnapshotJSON(t.Context(), fixture)
			if !errors.Is(err, ErrIncompatibleBackup) {
				t.Fatalf("validation error = %v; want incompatible", err)
			}
		})
	}
}

func TestRestoreJSONAllowedFieldsAndBoundaries(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`null`, `[]`, `[{"id":1}]`,
		`[{"id":9,"name":"quoted \\\" text","quantity":1,"price":2,"value_try":3,"value_usd":4,` +
			`"value_eur":null,"currency":"TRY","is_stale":false,"is_failed":false,"valuation_available":true}]`,
		`[{"id":1,"name":"` + strings.Repeat("a", restoreMaxStringBytes) + `"}]`,
	} {
		_, err := validateRestoreSnapshotJSON(t.Context(), raw)
		if err != nil {
			t.Fatalf("valid JSON rejected: %v", err)
		}
	}
}

//nolint:containedctx // Synthetic context counts preflight cancellation checks deterministically.
type restoreCancelContext struct {
	context.Context

	calls int
}

func (ctx *restoreCancelContext) Err() error {
	ctx.calls++
	if ctx.calls > 1 {
		return context.Canceled
	}

	return nil
}

func TestRestoreCancellationWithinSingleElement(t *testing.T) {
	t.Parallel()
	// This is one syntactically valid object. Cancellation after the initial
	// check must be observed during preflight, before JSON token allocation.
	ctx := &restoreCancelContext{Context: t.Context()}

	_, err := validateRestoreSnapshotJSON(
		ctx,
		`[{"id":1,`+strings.Repeat(" ", restoreMaxObjectBytes/2)+`"quantity":1}]`,
	)
	if !errors.Is(err, context.Canceled) || ctx.calls != 2 {
		t.Fatalf("error = %v; checks = %d", err, ctx.calls)
	}
}

func TestRestoreRejectsDDLBeforeIntegrity(t *testing.T) {
	t.Parallel()

	fixtures := map[string]string{
		"extra index": `CREATE INDEX unexpected ON holdings(lower(name))`,
		"changed index": `DROP INDEX idx_holdings_asset_type;
 CREATE INDEX idx_holdings_asset_type ON holdings(asset_type COLLATE NOCASE)`,
		"extra table": `CREATE TABLE unexpected (body TEXT CHECK(length(body)>0))`,
		"altered check": `ALTER TABLE settings RENAME TO old_settings;
 CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL CHECK(length(value)>0),
 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
 INSERT INTO settings SELECT * FROM old_settings; DROP TABLE old_settings`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			seedRestoreValidationData(t, source, "Backup")
			execRestoreFixture(t, source, fixture)
			assertBackupRejectedAtomically(t, source)
		})
	}
}

func TestRestoreRejectsSQLiteValueBeforeStructScan(t *testing.T) {
	t.Parallel()

	for name, query := range map[string]string{
		"snapshot": `UPDATE snapshots SET holdings_json = printf('%.*c', 1048577, ' ')`,
		"holding":  `UPDATE holdings SET notes = printf('%.*c', 4097, 'x')`,
		"setting":  `UPDATE settings SET value = printf('%.*c', 4097, 'x') WHERE key = 'marker'`,
		"row count": `WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM nums WHERE n < 100001)
 INSERT INTO holdings (name, asset_type) SELECT 'x', 'Cash' FROM nums`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := openTestDB(t, "source.db")
			seedRestoreValidationData(t, source, "Backup")
			execRestoreFixture(t, source, query)
			assertBackupRejectedAtomically(t, source)
		})
	}
}
