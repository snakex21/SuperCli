package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
	"strings"
)

// Retain the original indexed column, tokenizer, default detail and docsize.
// Only the source of stored text changes: messages already owns that text.
const messagesFTSSchema = `CREATE VIRTUAL TABLE messages_fts USING fts5(
	content,
	content = 'messages',
	content_rowid = 'id',
	tokenize = 'porter unicode61 remove_diacritics 2'
)`

const legacyMessagesFTSSchema = `CREATE VIRTUAL TABLE messages_fts USING fts5(
	content,
	tokenize = 'porter unicode61 remove_diacritics 2'
)`

var messagesFTSTriggers = [...]struct{ name, sql string }{
	{"messages_ai", `CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
	END`},
	{"messages_ad", `CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, COALESCE(old.content, ''));
	END`},
	{"messages_au", `CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, COALESCE(old.content, ''));
		INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
	END`},
}

var legacyMessagesFTSTriggers = [...]struct{ name, sql string }{
	{"messages_ai", `CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
	END`},
	{"messages_ad", `CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.id;
	END`},
	{"messages_au", `CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.id;
		INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
	END`},
}

const messagesFTSRebuild = `INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')`
const messagesFTSIntegrity = `INSERT INTO messages_fts(messages_fts, rank) VALUES ('integrity-check', 1)`

// A pinned connection is necessary because BEGIN IMMEDIATE must reserve the
// writer before examining the schema or rebuilding from messages.
type messagesFTSExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type messagesFTSState struct {
	exists  bool
	current bool
	legacy  bool
}

func (s *Store) ensureMessagesFTS() error {
	return s.ensureMessagesFTSWithMigration(migrateMessagesFTS)
}

// Keeping the replacement operation separate also permits fault injection
// around a real SQLite transaction in tests, without a Store test hook.
func (s *Store) ensureMessagesFTSWithMigration(migrate func(context.Context, messagesFTSExecutor) error) error {
	ctx := context.Background()
	state, err := readMessagesFTSState(ctx, s.db)
	if err != nil || state.current {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("messages FTS connection: %w", err)
	}
	defer conn.Close()
	if err := migrate(ctx, conn); err != nil {
		// Even a failed ROLLBACK must not return a connection with a live manual
		// transaction to the pool. Discard it on any migration failure.
		discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
		// Release this Conn's pool reservation before requesting the verification
		// connection, including when the pool permits only one open connection.
		closeErr := conn.Close()
		if closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
			return errors.Join(err, fmt.Errorf("release failed messages FTS connection: %w", closeErr))
		}
		var failure *messagesFTSMigrationError
		if !state.legacy || !errors.As(err, &failure) || !failure.rollbackConfirmed || !errors.Is(discardErr, driver.ErrBadConn) {
			return err
		}
		// An optional storage optimization must not disable a usable legacy
		// store. Re-check on a different connection, with a fresh snapshot and
		// writer reservation; both schema and all indexed text must still agree.
		if retainedErr := s.validateLegacyMessagesFTS(ctx); retainedErr != nil {
			return errors.Join(err, fmt.Errorf("cannot retain legacy messages FTS: %w", retainedErr))
		}
		log.Printf("session: messages FTS optimization postponed; retaining verified legacy index: %v", err)
		return nil
	}
	return nil
}

type messagesFTSMigrationError struct {
	cause             error
	rollbackConfirmed bool
}

func (e *messagesFTSMigrationError) Error() string { return e.cause.Error() }
func (e *messagesFTSMigrationError) Unwrap() error { return e.cause }

func migrateMessagesFTS(ctx context.Context, conn messagesFTSExecutor) (retErr error) {
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("reserve messages FTS writer: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// A canceled caller must still release the writer and restore the old
			// table and triggers. Do not reuse its context for the rollback.
			_, rollbackErr := conn.ExecContext(context.Background(), `ROLLBACK`)
			if rollbackErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("rollback messages FTS: %w", rollbackErr))
			}
			retErr = &messagesFTSMigrationError{cause: retErr, rollbackConfirmed: rollbackErr == nil}
		}
	}()
	// Another opener may have finished while we waited for the writer. This
	// check and all replacement DDL share the same transaction and connection.
	state, err := readMessagesFTSState(ctx, conn)
	if err != nil {
		return err
	}
	if !state.current {
		for _, trigger := range messagesFTSTriggers {
			if _, err := conn.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+trigger.name); err != nil {
				return fmt.Errorf("drop messages FTS trigger %s: %w", trigger.name, err)
			}
		}
		if state.exists {
			if _, err := conn.ExecContext(ctx, `DROP TABLE messages_fts`); err != nil {
				return fmt.Errorf("drop legacy messages FTS: %w", err)
			}
		}
		if _, err := conn.ExecContext(ctx, messagesFTSSchema); err != nil {
			return fmt.Errorf("create messages FTS: %w", err)
		}
		for _, trigger := range messagesFTSTriggers {
			if _, err := conn.ExecContext(ctx, trigger.sql); err != nil {
				return fmt.Errorf("create messages FTS trigger %s: %w", trigger.name, err)
			}
		}
		// Creating an external-content table does not index pre-existing rows.
		// Rebuild also repairs rows missed by missing or obsolete triggers.
		if _, err := conn.ExecContext(ctx, messagesFTSRebuild); err != nil {
			return fmt.Errorf("rebuild messages FTS: %w", err)
		}
		// rank=1 checks the index against messages, not just its internal pages.
		if _, err := conn.ExecContext(ctx, messagesFTSIntegrity); err != nil {
			return fmt.Errorf("verify messages FTS against messages: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit messages FTS: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) validateLegacyMessagesFTS(ctx context.Context) (retErr error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("legacy FTS verification connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		// Discard even if BEGIN failed, rather than assume transaction state
		// after a driver or I/O error.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return fmt.Errorf("reserve legacy FTS verification writer: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("rollback legacy FTS verification: %w", err))
			}
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	state, err := readMessagesFTSState(ctx, conn)
	if err != nil {
		return err
	}
	if !state.legacy {
		return fmt.Errorf("preserved table and all three triggers are not the exact legacy FTS schema")
	}
	// Internal FTS integrity checks its own content table, not messages. Match
	// every source ID and exact indexed byte string first, including NULL/empty
	// normalization, then check inverted-index consistency below.
	var differs bool
	err = conn.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM messages m LEFT JOIN messages_fts f ON f.rowid=m.id
		WHERE f.rowid IS NULL OR CAST(f.content AS BLOB) IS NOT CAST(COALESCE(m.content, '') AS BLOB)
		UNION ALL
		SELECT 1 FROM messages_fts f LEFT JOIN messages m ON m.id=f.rowid WHERE m.id IS NULL
	)`).Scan(&differs)
	if err != nil {
		return fmt.Errorf("compare legacy FTS with messages: %w", err)
	}
	if differs {
		return fmt.Errorf("legacy FTS has missing, orphaned or different message content")
	}
	if _, err := conn.ExecContext(ctx, messagesFTSIntegrity); err != nil {
		return fmt.Errorf("verify retained legacy FTS integrity: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("finish legacy FTS verification: %w", err)
	}
	committed = true
	return nil
}

func readMessagesFTSState(ctx context.Context, conn messagesFTSExecutor) (messagesFTSState, error) {
	rows, err := conn.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master
		WHERE name IN ('messages_fts', 'messages_ai', 'messages_ad', 'messages_au')`)
	if err != nil {
		return messagesFTSState{}, fmt.Errorf("read messages FTS schema: %w", err)
	}
	defer rows.Close()
	definitions := make(map[string]string, 4)
	for rows.Next() {
		var typ, name string
		var definition sql.NullString
		if err := rows.Scan(&typ, &name, &definition); err != nil {
			return messagesFTSState{}, fmt.Errorf("read messages FTS schema row: %w", err)
		}
		wantType := "trigger"
		if name == "messages_fts" {
			wantType = "table"
		}
		if typ != wantType || !definition.Valid {
			return messagesFTSState{}, fmt.Errorf("unsupported messages FTS schema object %s", name)
		}
		definitions[name] = normalizeMessagesFTSSQL(definition.String)
	}
	if err := rows.Err(); err != nil {
		return messagesFTSState{}, fmt.Errorf("read messages FTS schema rows: %w", err)
	}
	table, exists := definitions["messages_fts"]
	current := exists && table == normalizeMessagesFTSSQL(messagesFTSSchema)
	legacy := exists && table == normalizeMessagesFTSSQL(legacyMessagesFTSSchema)
	if exists && !current && !legacy {
		// Never guess a custom tokenizer, indexed column or content source.
		return messagesFTSState{}, fmt.Errorf("unsupported messages FTS table schema; existing history and index were preserved")
	}
	for _, trigger := range messagesFTSTriggers {
		current = current && definitions[trigger.name] == normalizeMessagesFTSSQL(trigger.sql)
	}
	for _, trigger := range legacyMessagesFTSTriggers {
		legacy = legacy && definitions[trigger.name] == normalizeMessagesFTSSQL(trigger.sql)
	}
	return messagesFTSState{exists: exists, current: current, legacy: legacy}, nil
}

// Ignore SQL keyword case and formatting, but preserve quoted values exactly.
// This recognizes schemas emitted by the application, not arbitrary FTS SQL.
func normalizeMessagesFTSSQL(statement string) string {
	var out strings.Builder
	quoted := false
	for i := 0; i < len(statement); i++ {
		c := statement[i]
		if c == '\'' {
			out.WriteByte(c)
			if quoted && i+1 < len(statement) && statement[i+1] == '\'' {
				i++
				out.WriteByte('\'')
			} else {
				quoted = !quoted
			}
			continue
		}
		if !quoted {
			switch c {
			case ' ', '\t', '\r', '\n', '\f', '\v':
				continue
			}
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
		}
		out.WriteByte(c)
	}
	return strings.TrimSuffix(out.String(), ";")
}
