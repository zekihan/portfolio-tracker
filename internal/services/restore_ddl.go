package services

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/zekihan/portfolio-tracker/internal/db"
)

const (
	restoreMaxSchemaObjects = 64
	restoreMaxDDLBytes      = 8192
)

type restoreSchemaObject struct {
	Name  string         `db:"name"`
	Kind  string         `db:"type"`
	Table string         `db:"tbl_name"`
	SQL   sql.NullString `db:"sql"`
}

// Derive the allowlist exclusively from application migrations, never from an
// uploaded database or a live database that may have custom indexes.
func approvedRestoreDDL(ctx context.Context, version int) (map[string]restoreSchemaObject, error) {
	database, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open approved schema: %w", err)
	}
	defer func() { _ = database.Close() }()

	database.SetMaxOpenConns(1)

	for _, migration := range db.Migrations[:version] {
		_, err = database.ExecContext(ctx, migration)
		if err != nil {
			return nil, fmt.Errorf("build approved schema: %w", err)
		}
	}

	objects, err := readRestoreDDL(ctx, database, "main")
	if err != nil {
		return nil, err
	}

	approved := make(map[string]restoreSchemaObject, len(objects))
	for _, object := range objects {
		approved[object.Name] = object
	}

	return approved, nil
}

func readRestoreDDL(
	ctx context.Context,
	conn sqlx.QueryerContext,
	schema string,
) ([]restoreSchemaObject, error) {
	var invalid int

	err := sqlx.GetContext(
		ctx,
		conn,
		&invalid,
		"SELECT EXISTS(SELECT 1 FROM "+schema+".sqlite_schema WHERE length(CAST(sql AS BLOB)) > ? "+
			"OR length(CAST(name AS BLOB)) > ? OR length(CAST(tbl_name AS BLOB)) > ?)",
		restoreMaxDDLBytes,
		restoreMaxStringBytes,
		restoreMaxStringBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect schema sizes: %w", ErrIncompatibleBackup, err)
	}

	if invalid != 0 {
		return nil, fmt.Errorf("%w: schema byte limit", ErrIncompatibleBackup)
	}

	var objects []restoreSchemaObject

	err = sqlx.SelectContext(
		ctx,
		conn,
		&objects,
		"SELECT name, type, tbl_name, sql FROM "+schema+".sqlite_schema LIMIT ?",
		restoreMaxSchemaObjects+1,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: read schema: %w", ErrIncompatibleBackup, err)
	}

	if len(objects) > restoreMaxSchemaObjects {
		return nil, fmt.Errorf("%w: schema object limit", ErrIncompatibleBackup)
	}

	return objects, nil
}

func validateRestoreDDL(ctx context.Context, conn sqlx.QueryerContext, version int) error {
	approved, err := approvedRestoreDDL(ctx, version)
	if err != nil {
		return err
	}

	objects, err := readRestoreDDL(ctx, conn, restoreSchema)
	if err != nil {
		return err
	}

	for _, object := range objects {
		expected, exists := approved[object.Name]
		if !exists || expected.Kind != object.Kind || expected.Table != object.Table ||
			expected.SQL.Valid != object.SQL.Valid ||
			normalizeRestoreDDL(expected.SQL.String) != normalizeRestoreDDL(object.SQL.String) {
			return fmt.Errorf("%w: unapproved schema object", ErrIncompatibleBackup)
		}
	}

	for name, expected := range approved {
		if name != "sqlite_sequence" && !slices.Contains(restoreTables, expected.Table) {
			continue
		}

		present := slices.ContainsFunc(
			objects,
			func(object restoreSchemaObject) bool { return object.Name == name },
		)
		if !present {
			return fmt.Errorf("%w: missing approved schema object", ErrIncompatibleBackup)
		}
	}

	return nil
}

// Ignore layout and keyword case, but preserve quoted identifiers and literals.
// In particular, CHECK expressions and index expressions must match exactly.
func normalizeRestoreDDL(statement string) string {
	var (
		normalized strings.Builder
		quote      byte
	)

	for index := 0; index < len(statement); index++ {
		char := statement[index]
		if quote != 0 {
			normalized.WriteByte(char)

			if char == quote {
				if index+1 < len(statement) && statement[index+1] == quote {
					index++

					normalized.WriteByte(quote)
				} else {
					quote = 0
				}
			}

			continue
		}

		switch char {
		case '\'', '"', '`':
			quote = char
		case ' ', '\n', '\r', '\t':
			continue
		default:
			if char >= 'A' && char <= 'Z' {
				char += 'a' - 'A'
			}
		}

		normalized.WriteByte(char)
	}

	return normalized.String()
}
