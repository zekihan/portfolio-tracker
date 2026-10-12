package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ErrIncompatibleBackup is returned when an uploaded database cannot replace
// the live data: it is corrupt, from a different schema version, or missing
// tables or columns.
var ErrIncompatibleBackup = errors.New("backup is not compatible with this database")

// restoreTables are the user-data tables replaced by a restore. Provider_registry
// is configuration and is kept.
var restoreTables = []string{"holdings", "snapshots", "price_cache", "settings"}

const restoreSchema = "restore_src"

// RestoreFrom replaces holdings, snapshots, cached prices, and settings with the
// contents of the SQLite database at path. The file is validated first and the
// replacement runs in one transaction, so a failure leaves the live data unchanged.
func (service *SettingsService) RestoreFrom(ctx context.Context, path string) error {
	conn, err := service.db.Connx(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}

	defer func() {
		_ = conn.Close()
	}()

	// ATTACH cannot run inside a transaction, so it happens before BEGIN.
	_, err = conn.ExecContext(ctx, "ATTACH DATABASE ? AS "+restoreSchema, path)
	if err != nil {
		return fmt.Errorf(
			"%w: %w",
			ErrIncompatibleBackup,
			err,
		)
	}

	defer func() {
		//nolint:errcheck // Detach is best effort; the connection is reused by the pool
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE "+restoreSchema)
	}()

	err = validateRestoreSource(ctx, conn)
	if err != nil {
		return err
	}

	return replaceFromRestoreSource(ctx, conn)
}

func validateRestoreSource(ctx context.Context, conn *sqlx.Conn) error {
	var quickCheck string

	err := sqlx.GetContext(ctx, conn, &quickCheck, "PRAGMA "+restoreSchema+".quick_check")
	if err != nil {
		return fmt.Errorf(
			"%w: %w",
			ErrIncompatibleBackup,
			err,
		)
	}

	if quickCheck != "ok" {
		return fmt.Errorf("%w: integrity check failed", ErrIncompatibleBackup)
	}

	var sourceVersion, liveVersion int

	err = sqlx.GetContext(ctx, conn, &sourceVersion, "PRAGMA "+restoreSchema+".user_version")
	if err != nil {
		return fmt.Errorf("read backup schema version: %w", err)
	}

	err = sqlx.GetContext(ctx, conn, &liveVersion, "PRAGMA main.user_version")
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	// Version 10 adds operational snapshot coordination only; user tables are unchanged.
	const legacyVersion, coordinatedVersion = 9, 10
	if sourceVersion != liveVersion &&
		(sourceVersion != legacyVersion || liveVersion != coordinatedVersion) {
		return fmt.Errorf("%w: schema version %d, expected %d",
			ErrIncompatibleBackup, sourceVersion, liveVersion)
	}

	for _, table := range restoreTables {
		liveColumns, columnsErr := tableColumns(ctx, conn, "main", table)
		if columnsErr != nil {
			return columnsErr
		}

		sourceColumns, columnsErr := tableColumns(ctx, conn, restoreSchema, table)
		if columnsErr != nil {
			return columnsErr
		}

		for _, column := range liveColumns {
			if !containsString(sourceColumns, column) {
				return fmt.Errorf(
					"%w: table %s has no column %s",
					ErrIncompatibleBackup,
					table,
					column,
				)
			}
		}
	}

	return nil
}

func replaceFromRestoreSource(ctx context.Context, conn *sqlx.Conn) error {
	tx, err := conn.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restore: %w", err)
	}

	defer func() {
		//nolint:errcheck // Rollback after a successful commit returns an error that is not actionable
		_ = tx.Rollback()
	}()

	err = validateRestoreData(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(
		ctx,
		"UPDATE main.snapshot_state SET generation = generation + 1 WHERE id = 1",
	)
	if err != nil {
		return fmt.Errorf("invalidate snapshots before restore: %w", err)
	}

	for _, table := range restoreTables {
		_, err = tx.ExecContext(ctx, "DELETE FROM main."+table)
		if err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}

	for _, table := range restoreTables {
		columns, columnsErr := tableColumns(ctx, tx, "main", table)
		if columnsErr != nil {
			return columnsErr
		}

		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = quoteIdentifier(column)
		}

		list := strings.Join(quoted, ", ")
		statement := fmt.Sprintf("INSERT INTO main.%[1]s (%[2]s) SELECT %[2]s FROM %[3]s.%[1]s",
			table, list, restoreSchema)

		_, err = tx.ExecContext(ctx, statement)
		if err != nil {
			return fmt.Errorf("%w: restore %s: %w", ErrIncompatibleBackup, table, err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit restore: %w", err)
	}

	return nil
}

func tableColumns(
	ctx context.Context,
	conn sqlx.QueryerContext,
	schema, table string,
) ([]string, error) {
	var columns []string

	rows, err := conn.QueryxContext(ctx,
		"SELECT name FROM pragma_table_info(?, ?)", table, schema)
	if err != nil {
		return nil, fmt.Errorf("read columns of %s.%s: %w", schema, table, err)
	}

	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var name string

		scanErr := rows.Scan(&name)
		if scanErr != nil {
			return nil, fmt.Errorf("scan column of %s.%s: %w", schema, table, scanErr)
		}

		columns = append(columns, name)
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("read columns of %s.%s: %w", schema, table, err)
	}

	if len(columns) == 0 {
		return nil, fmt.Errorf(
			"%w: table %s is missing in %s",
			ErrIncompatibleBackup,
			table,
			schema,
		)
	}

	return columns, nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}
