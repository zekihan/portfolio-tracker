package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
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
func (service *SettingsService) RestoreFrom(ctx context.Context, path string) (result error) {
	conn, err := service.db.Connx(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}

	defer func() {
		_ = conn.Close()
	}()

	// Install cleanup before ATTACH: cancellation can report an error after SQLite
	// has attached the source. Never return an attached connection to the pool.
	defer func() {
		result = errors.Join(result, service.cleanupRestoreConnection(ctx, conn))
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

	return replaceFromRestoreSource(ctx, conn)
}

// A cleanup failure must never put an attached connection back in circulation.
// Close the pool before releasing this connection: otherwise database/sql could
// silently replace a discarded in-memory database with an empty one.
func (service *SettingsService) cleanupRestoreConnection(
	ctx context.Context,
	conn *sqlx.Conn,
) error {
	err := detachRestoreSource(ctx, conn)
	if err == nil {
		return nil
	}

	closeErr := service.db.Close()

	return fmt.Errorf("restore cleanup failed; database closed to prevent unsafe reuse: %w",
		errors.Join(err, closeErr))
}

// Cleanup is independent of request cancellation but has a bounded lifetime.
func detachRestoreSource(parent context.Context, conn *sqlx.Conn) error {
	const cleanupTimeout = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), cleanupTimeout)
	defer cancel()

	var attached int

	err := conn.GetContext(ctx, &attached,
		"SELECT COUNT(*) FROM pragma_database_list WHERE name = ?", restoreSchema)
	if err != nil {
		return fmt.Errorf("inspect restore attachment: %w", err)
	}

	if attached == 0 {
		return nil
	}

	_, err = conn.ExecContext(ctx, "DETACH DATABASE "+restoreSchema)
	if err != nil {
		return fmt.Errorf("detach restore source: %w", err)
	}

	return nil
}

func validateRestoreSource(ctx context.Context, conn sqlx.QueryerContext) error {
	err := validateRestoreStructures(ctx, conn)
	if err != nil {
		return err
	}

	var integrityCheck string

	err = sqlx.GetContext(ctx, conn, &integrityCheck, "PRAGMA "+restoreSchema+".integrity_check")
	if err != nil {
		return fmt.Errorf(
			"%w: %w",
			ErrIncompatibleBackup,
			err,
		)
	}

	if integrityCheck != "ok" {
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

	// Versions 10 and 11 add local operational tables only; user tables are unchanged.
	const legacyVersion, receiptVersion = 9, 11
	if sourceVersion != liveVersion &&
		(sourceVersion < legacyVersion || sourceVersion > liveVersion || liveVersion > receiptVersion) {
		return fmt.Errorf("%w: schema version %d, expected %d",
			ErrIncompatibleBackup, sourceVersion, liveVersion)
	}

	return nil
}

func replaceFromRestoreSource(ctx context.Context, conn *sqlx.Conn) error {
	// Request cancellation must not start database/sql's asynchronous rollback:
	// it can race DETACH and discard the sole connection of an in-memory DB.
	// Statements still honor ctx; our deferred rollback finishes synchronously.
	tx, err := conn.BeginTxx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return fmt.Errorf("begin restore: %w", err)
	}

	defer func() {
		//nolint:errcheck // Rollback after a successful commit returns an error that is not actionable
		_ = tx.Rollback()
	}()

	err = db.CheckPortfolioGeneration(ctx, tx)
	if err != nil {
		return fmt.Errorf("check restore generation: %w", err)
	}

	// Hold the same source read snapshot for structure/data validation and copying.
	err = validateRestoreSource(ctx, tx)
	if err != nil {
		return err
	}

	historicalID, err := validateRestoreData(ctx, tx)
	if err != nil {
		return err
	}

	sequences, err := restoreSequenceHighWater(ctx, tx, historicalID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(
		ctx,
		"UPDATE main.snapshot_state SET generation = generation + 1 WHERE id = 1; "+
			"DELETE FROM main.holding_create_receipts",
	)
	if err != nil {
		return fmt.Errorf("invalidate snapshots before restore: %w", err)
	}

	err = copyRestoreTables(ctx, tx)
	if err != nil {
		return err
	}

	err = preserveRestoreSequences(ctx, tx, sequences)
	if err != nil {
		return err
	}

	err = ctx.Err()
	if err != nil {
		return fmt.Errorf("commit restore: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit restore: %w", err)
	}

	return nil
}

func copyRestoreTables(ctx context.Context, tx *sqlx.Tx) error {
	for _, table := range restoreTables {
		_, err := tx.ExecContext(ctx, "DELETE FROM main."+table)
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
		statement := fmt.Sprintf(
			"INSERT INTO main.%[1]s (%[2]s) SELECT %[2]s FROM %[3]s.%[1]s NOT INDEXED",
			table,
			list,
			restoreSchema,
		)

		_, err := tx.ExecContext(ctx, statement)
		if err != nil {
			return fmt.Errorf("%w: restore %s: %w", ErrIncompatibleBackup, table, err)
		}
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
