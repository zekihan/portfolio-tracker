package services

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"modernc.org/sqlite"

	sqlitelib "modernc.org/sqlite/lib"
)

// Apply before ATTACH, so even SQLite schema parsing and row decoding are
// bounded. These connection-local limits never modify the server connection.
func limitRestoreSQLite(conn *sqlx.Conn) error {
	const (
		maxSQLiteValueBytes  = 2 << 20
		maxSQLBytes          = 64 << 10
		maxColumns           = 64
		maxExpressionDepth   = 64
		maxVMOperations      = 10000
		maxFunctionArguments = 16
		maxVariables         = 64
	)

	limits := []struct{ kind, value int }{
		{sqlitelib.SQLITE_LIMIT_LENGTH, maxSQLiteValueBytes},
		{sqlitelib.SQLITE_LIMIT_SQL_LENGTH, maxSQLBytes},
		{sqlitelib.SQLITE_LIMIT_COLUMN, maxColumns},
		{sqlitelib.SQLITE_LIMIT_EXPR_DEPTH, maxExpressionDepth},
		{sqlitelib.SQLITE_LIMIT_VDBE_OP, maxVMOperations},
		{sqlitelib.SQLITE_LIMIT_FUNCTION_ARG, maxFunctionArguments},
		{sqlitelib.SQLITE_LIMIT_ATTACHED, 1},
		{sqlitelib.SQLITE_LIMIT_VARIABLE_NUMBER, maxVariables},
		{sqlitelib.SQLITE_LIMIT_LIKE_PATTERN_LENGTH, restoreMaxStringBytes},
		{sqlitelib.SQLITE_LIMIT_WORKER_THREADS, 0},
	}
	for _, limit := range limits {
		_, err := sqlite.Limit(conn.Conn, limit.kind, limit.value)
		if err != nil {
			return fmt.Errorf("limit restore SQLite connection: %w", err)
		}
	}

	return nil
}
