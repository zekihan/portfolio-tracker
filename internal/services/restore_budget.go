package services

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"
)

const (
	restoreMaxFileBytes      = 256 << 20
	restoreMaxJSONBytes      = 1 << 20
	restoreMaxTotalJSONBytes = 64 << 20
	restoreMaxStringBytes    = 4096
	restoreMaxObjectBytes    = 64 << 10
	restoreMaxRows           = 100000
	restoreMaxHoldings       = 4096
	restoreMaxFields         = 11
	restoreCancelInterval    = 1024
)

var restoreHoldingFields = []string{
	"id", "quantity", "price", "value_try", "value_usd", "value_eur", "name", "currency",
	"is_stale", "is_failed", "valuation_available",
}

// Scan without allocating tokens, maps or nested values. The JSON decoder then
// checks syntax and types, with every token and object already bounded.
type restoreJSONBudget struct {
	depth, start, stringStart, fields, holdings int
	escaped                                     bool
}

func preflightRestoreJSON(ctx context.Context, raw string) error {
	if len(raw) > restoreMaxJSONBytes {
		return fmt.Errorf("%w: snapshot JSON byte limit", ErrIncompatibleBackup)
	}

	budget := restoreJSONBudget{stringStart: -1}

	for offset := range len(raw) {
		if offset%restoreCancelInterval == 0 {
			err := ctx.Err()
			if err != nil {
				return fmt.Errorf("validate snapshot: %w", err)
			}
		}

		err := budget.accept(raw[offset], offset)
		if err != nil {
			return err
		}
	}

	return nil
}

func (budget *restoreJSONBudget) accept(char byte, offset int) error {
	if budget.depth == 2 && offset-budget.start+1 > restoreMaxObjectBytes {
		return fmt.Errorf("%w: snapshot object byte limit", ErrIncompatibleBackup)
	}

	if budget.stringStart >= 0 {
		return budget.acceptString(char, offset)
	}

	switch char {
	case '"':
		budget.stringStart = offset
	case '{', '[':
		budget.depth++
		if budget.depth > 2 {
			return fmt.Errorf("%w: snapshot nesting limit", ErrIncompatibleBackup)
		}

		if budget.depth == 2 {
			if char != '{' {
				return fmt.Errorf("%w: snapshot element must be an object", ErrIncompatibleBackup)
			}

			budget.start, budget.fields = offset, 0

			budget.holdings++
			if budget.holdings > restoreMaxHoldings {
				return fmt.Errorf("%w: snapshot element limit", ErrIncompatibleBackup)
			}
		}
	case '}', ']':
		budget.depth--
	case ':':
		budget.fields++
		if budget.fields > restoreMaxFields {
			return fmt.Errorf("%w: snapshot field limit", ErrIncompatibleBackup)
		}
	default:
	}

	return nil
}

func (budget *restoreJSONBudget) acceptString(char byte, offset int) error {
	if offset-budget.stringStart-1 > restoreMaxStringBytes {
		return fmt.Errorf("%w: snapshot string limit", ErrIncompatibleBackup)
	}

	switch {
	case budget.escaped:
		budget.escaped = false
	case char == '\\':
		budget.escaped = true
	case char == '"':
		budget.stringStart = -1
	default:
	}

	return nil
}

func decodeRestoreFields(decoder *json.Decoder) (map[string]json.RawMessage, error) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%w: snapshot holding must be an object", ErrIncompatibleBackup)
	}

	fields := make(map[string]json.RawMessage, restoreMaxFields)

	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode snapshot field: %w", err)
		}

		key, valid := token.(string)
		if !valid || !slices.Contains(restoreHoldingFields, key) {
			return nil, fmt.Errorf("%w: unknown snapshot field", ErrIncompatibleBackup)
		}

		if _, duplicate := fields[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate snapshot field", ErrIncompatibleBackup)
		}

		var value json.RawMessage

		err = decoder.Decode(&value)
		if err != nil {
			return nil, fmt.Errorf("decode snapshot value: %w", err)
		}

		fields[key] = value
	}

	_, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode snapshot object: %w", err)
	}

	return fields, nil
}

// SQLite length(blob) reads the stored byte length without materializing the
// value in Go. Only bounded rows reach StructScan or the JSON decoder.
func validateRestoreBudgets(ctx context.Context, conn sqlx.QueryerContext) error {
	var totalRows, totalJSON int64

	for _, table := range restoreTables {
		var count int64

		err := sqlx.GetContext(
			ctx,
			conn,
			&count,
			"SELECT COUNT(*) FROM (SELECT 1 FROM "+restoreSchema+"."+table+" NOT INDEXED LIMIT ?)",
			restoreMaxRows+1,
		)
		if err != nil {
			return fmt.Errorf("%w: count backup rows: %w", ErrIncompatibleBackup, err)
		}

		totalRows += count
		if totalRows > restoreMaxRows {
			return fmt.Errorf("%w: backup row limit", ErrIncompatibleBackup)
		}

		columns, err := tableColumns(ctx, conn, restoreSchema, table)
		if err != nil {
			return err
		}

		for _, column := range columns {
			limit := restoreMaxStringBytes
			if table == restoreSnapshotsTable && column == "holdings_json" {
				limit = restoreMaxJSONBytes
			}

			var oversized int

			query := "SELECT EXISTS(SELECT 1 FROM " + restoreSchema + "." + table +
				" NOT INDEXED WHERE length(CAST(" + quoteIdentifier(column) + " AS BLOB)) > ?)"

			err = sqlx.GetContext(ctx, conn, &oversized, query, limit)
			if err != nil {
				return fmt.Errorf("%w: inspect backup value sizes: %w", ErrIncompatibleBackup, err)
			}

			if oversized != 0 {
				return fmt.Errorf("%w: backup value byte limit", ErrIncompatibleBackup)
			}
		}
	}

	err := sqlx.GetContext(
		ctx,
		conn,
		&totalJSON,
		"SELECT COALESCE(SUM(length(CAST(holdings_json AS BLOB))), 0) FROM restore_src.snapshots NOT INDEXED",
	)
	if err != nil {
		return fmt.Errorf("%w: count snapshot bytes: %w", ErrIncompatibleBackup, err)
	}

	if totalJSON > restoreMaxTotalJSONBytes {
		return fmt.Errorf("%w: cumulative snapshot byte limit", ErrIncompatibleBackup)
	}

	return nil
}
