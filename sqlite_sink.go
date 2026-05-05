//go:build sqlite

package orchid

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY,
    type        TEXT    NOT NULL,
    timestamp   TEXT    NOT NULL,
    process_id  TEXT    NOT NULL,
    tool        TEXT,
    effect_id   TEXT,
    result      TEXT,
    tokens_in   INTEGER,
    tokens_out  INTEGER,
    duration_ms INTEGER,
    data        TEXT    NOT NULL CHECK(json_valid(data))
);
CREATE INDEX IF NOT EXISTS idx_events_type       ON events(type);
CREATE INDEX IF NOT EXISTS idx_events_process_id ON events(process_id);
CREATE INDEX IF NOT EXISTS idx_events_tool       ON events(tool);
CREATE INDEX IF NOT EXISTS idx_events_effect_id  ON events(effect_id);
PRAGMA user_version = 1;
`

const insertSQL = `INSERT INTO events (id, type, timestamp, process_id, tool, effect_id, result, tokens_in, tokens_out, duration_ms, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// SQLiteSink writes events to a SQLite database.
type SQLiteSink struct {
	db   *sql.DB
	mu   sync.Mutex
	stmt *sql.Stmt
}

func newSQLiteSink(path string) (*SQLiteSink, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, err
	}
	stmt, err := db.Prepare(insertSQL)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteSink{db: db, stmt: stmt}, nil
}

func (s *SQLiteSink) Append(e Event) {
	payload, err := json.Marshal(e.Data)
	if err != nil || string(payload) == "null" {
		payload = []byte("{}")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stmt == nil {
		return
	}
	_, _ = s.stmt.Exec(
		int64(e.ID),
		e.Type.String(),
		e.Timestamp.Format(time.RFC3339Nano),
		e.ProcessID,
		nullIfEmpty(e.Data["tool"]),
		nullIfEmpty(e.Data["effect_id"]),
		nullIfEmpty(e.Data["result"]),
		atoiOrNil(e.Data["input_tokens"]),
		atoiOrNil(e.Data["output_tokens"]),
		atoi64OrNil(e.Data["duration_ms"]),
		string(payload),
	)
}

func (s *SQLiteSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	if s.stmt != nil {
		errs = append(errs, s.stmt.Close())
		s.stmt = nil
	}
	if s.db != nil {
		errs = append(errs, s.db.Close())
		s.db = nil
	}
	return errors.Join(errs...)
}

// nullIfEmpty returns nil (SQL NULL) if s is empty, otherwise s.
func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// atoiOrNil returns nil if s is empty or not a valid int, otherwise the int value.
func atoiOrNil(s string) interface{} {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return v
}

// atoi64OrNil returns nil if s is empty or not a valid int64, otherwise the int64 value.
func atoi64OrNil(s string) interface{} {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return v
}

func sqliteAvailable() bool { return true }
