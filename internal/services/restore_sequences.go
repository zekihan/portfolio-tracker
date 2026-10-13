package services

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/jmoiron/sqlx"
)

type restoreSequence struct {
	table string
	value int64
}

// Recover IDs even when older restores discarded sqlite_sequence. Keep the live
// high-water marks as well so replacement never rolls an identity counter back.
func restoreSequenceHighWater(
	ctx context.Context,
	tx *sqlx.Tx,
	historicalID int64,
) ([]restoreSequence, error) {
	liveHistoricalID, err := liveRestoreHistoricalID(ctx, tx)
	if err != nil {
		return nil, err
	}

	historicalID = max(historicalID, liveHistoricalID)

	sequences := []restoreSequence{
		{table: "holdings", value: historicalID},
		{table: "snapshots"},
		{table: "price_cache"},
	}
	for _, schema := range []string{"main", restoreSchema} {
		for index := range sequences {
			sequence := &sequences[index]

			value, err := restoreTableHighWater(ctx, tx, schema, sequence.table)
			if err != nil {
				return nil, err
			}

			sequence.value = max(sequence.value, value)
		}
	}

	return sequences, nil
}

func restoreTableHighWater(ctx context.Context, tx *sqlx.Tx, schema, table string) (int64, error) {
	if schema == restoreSchema {
		err := validateRestoreSequence(ctx, tx, table)
		if err != nil {
			return 0, err
		}
	}

	var value int64

	// A valid backup must be able to repair live metadata poisoned by an older
	// restore. Discard only invalid/exhausted live values; retain every usable
	// high-water mark. Source rows and sequences have already been validated.
	err := tx.GetContext(ctx, &value, `SELECT MAX(
COALESCE((SELECT MAX(seq) FROM `+schema+`.sqlite_sequence
WHERE name = ? AND typeof(seq) = 'integer' AND seq >= 0 AND seq < ?), 0),
COALESCE((SELECT MAX(id) FROM `+schema+`.`+table+` NOT INDEXED WHERE id > 0 AND id < ?), 0))`,
		table, int64(math.MaxInt64), int64(math.MaxInt64))
	if err != nil {
		return 0, fmt.Errorf("%w: read %s high-water mark: %w", ErrIncompatibleBackup, table, err)
	}

	return value, nil
}

func validateRestoreSequence(ctx context.Context, tx *sqlx.Tx, table string) error {
	var invalid, count int

	err := tx.GetContext(ctx, &invalid, `SELECT COUNT(*) FROM restore_src.sqlite_sequence
WHERE name = ? AND (typeof(seq) != 'integer' OR seq < 0 OR seq >= ?)`, table, int64(math.MaxInt64))
	if err != nil {
		return fmt.Errorf("%w: read restored sequences: %w", ErrIncompatibleBackup, err)
	}

	err = tx.GetContext(ctx, &count,
		"SELECT COUNT(*) FROM restore_src.sqlite_sequence WHERE name = ?", table)
	if err != nil {
		return fmt.Errorf("%w: count restored sequences: %w", ErrIncompatibleBackup, err)
	}

	if invalid != 0 || count > 1 {
		return fmt.Errorf("%w: invalid restored sequence for %s", ErrIncompatibleBackup, table)
	}

	return nil
}

func preserveRestoreSequences(ctx context.Context, tx *sqlx.Tx, sequences []restoreSequence) error {
	for _, sequence := range sequences {
		// Rebuild the single row so corrupted duplicate live entries cannot survive.
		_, err := tx.ExecContext(
			ctx,
			"DELETE FROM main.sqlite_sequence WHERE name = ?",
			sequence.table,
		)
		if err != nil {
			return fmt.Errorf("reset restored sequence: %w", err)
		}

		_, err = tx.ExecContext(ctx, "INSERT INTO main.sqlite_sequence (name, seq) VALUES (?, ?)",
			sequence.table, sequence.value)
		if err != nil {
			return fmt.Errorf("preserve restored sequence: %w", err)
		}
	}

	return nil
}

// Old live databases may have lost their sequence too. Recover usable IDs even
// when other history entries are corrupt, so a good backup can still repair them.
func liveRestoreHistoricalID(ctx context.Context, tx *sqlx.Tx) (int64, error) {
	rows, err := tx.QueryxContext(ctx, "SELECT holdings_json FROM main.snapshots NOT INDEXED")
	if err != nil {
		return 0, fmt.Errorf("read live historical identities: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var highWater int64

	for rows.Next() {
		var raw string

		err = rows.Scan(&raw)
		if err != nil {
			return 0, fmt.Errorf("read live historical identity: %w", err)
		}

		id, decodeErr := recoverRestoreHistoricalID(ctx, raw)
		if decodeErr != nil {
			return 0, decodeErr
		}

		highWater = max(highWater, id)
	}

	err = rows.Err()
	if err != nil {
		return 0, fmt.Errorf("read live historical identities: %w", err)
	}

	return highWater, nil
}

func recoverRestoreHistoricalID(ctx context.Context, raw string) (int64, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))

	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return 0, wrapOperationError("recover live historical identities", ctx.Err())
	}

	var highWater int64

	for decoder.More() {
		cancelErr := ctx.Err()
		if cancelErr != nil {
			return 0, fmt.Errorf("recover live historical identities: %w", cancelErr)
		}

		var entry json.RawMessage

		err = decoder.Decode(&entry)
		if err != nil {
			break
		}

		var holding map[string]json.RawMessage

		err = json.Unmarshal(entry, &holding)
		if err != nil {
			continue
		}

		var id int64

		err = json.Unmarshal(holding["id"], &id)
		if err == nil && restoreValidID(id) {
			highWater = max(highWater, id)
		}
	}

	return highWater, wrapOperationError("recover live historical identities", ctx.Err())
}
