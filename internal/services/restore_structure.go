package services

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/jmoiron/sqlx"
)

type restoreColumn struct {
	Name    string         `db:"name"`
	Type    string         `db:"type"`
	Default sql.NullString `db:"dflt_value"`
	NotNull int            `db:"notnull"`
	PK      int            `db:"pk"`
	Hidden  int            `db:"hidden"`
}

// Only ordinary stored tables can guarantee identical validation and copy reads.
// Views, virtual tables, generated columns, and triggers are not backup formats.
func validateRestoreStructures(
	ctx context.Context,
	conn sqlx.QueryerContext,
	sourceVersion int,
) error {
	var unsupported int

	err := sqlx.GetContext(ctx, conn, &unsupported, `SELECT
(SELECT COUNT(*) FROM restore_src.sqlite_schema WHERE type NOT IN ('table', 'index')) +
(SELECT COUNT(*) FROM pragma_table_list WHERE schema = ? AND type != 'table') +
(SELECT COUNT(*) FROM main.sqlite_schema WHERE type = 'trigger'
AND tbl_name IN ('holdings', 'snapshots', 'price_cache', 'settings', 'snapshot_state',
'holding_create_receipts'))`, restoreSchema)
	if err != nil {
		return fmt.Errorf("%w: inspect backup structures: %w", ErrIncompatibleBackup, err)
	}

	if unsupported != 0 {
		return fmt.Errorf("%w: unsupported database structure", ErrIncompatibleBackup)
	}

	for _, table := range append(slices.Clone(restoreTables), "sqlite_sequence") {
		live, columnsErr := restoreTableStructure(ctx, conn, "main", table)
		if columnsErr != nil {
			return columnsErr
		}

		source, columnsErr := restoreTableStructure(ctx, conn, restoreSchema, table)
		if columnsErr != nil {
			return columnsErr
		}

		if table == restoreSnapshotsTable && sourceVersion < restoreEURVersion {
			live = slices.DeleteFunc(live, func(column restoreColumn) bool {
				return column.Name == "total_value_eur" || column.Name == "eur_try_rate"
			})
		}

		if !slices.Equal(live, source) {
			return fmt.Errorf("%w: unexpected structure of %s", ErrIncompatibleBackup, table)
		}
	}

	return nil
}

func restoreTableStructure(
	ctx context.Context,
	conn sqlx.QueryerContext,
	schema, table string,
) ([]restoreColumn, error) {
	var ordinary int

	err := sqlx.GetContext(ctx, conn, &ordinary, `SELECT COUNT(*) FROM pragma_table_list
WHERE schema = ? AND name = ? AND type = 'table' AND wr = 0`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect table %s: %w", ErrIncompatibleBackup, table, err)
	}

	if ordinary != 1 {
		return nil, fmt.Errorf("%w: missing ordinary table %s", ErrIncompatibleBackup, table)
	}

	var columns []restoreColumn

	err = sqlx.SelectContext(
		ctx,
		conn,
		&columns,
		`SELECT name, type, "notnull", dflt_value, pk, hidden
FROM pragma_table_xinfo(?, ?) ORDER BY cid`,
		table,
		schema,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect columns of %s: %w", ErrIncompatibleBackup, table, err)
	}

	for index := range columns {
		if columns[index].Hidden != 0 {
			return nil, fmt.Errorf(
				"%w: generated or hidden column in %s",
				ErrIncompatibleBackup,
				table,
			)
		}

		columns[index].Type = strings.ToUpper(strings.TrimSpace(columns[index].Type))
	}

	return columns, nil
}
