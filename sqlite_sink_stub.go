//go:build !sqlite

package orchid

import "errors"

var errSQLiteUnavailable = errors.New("orchid: SQLite sink not compiled in (rebuild with -tags sqlite)")

// SQLiteSink is a stub when built without the sqlite tag.
type SQLiteSink struct{}

func newSQLiteSink(string) (*SQLiteSink, error) { return nil, errSQLiteUnavailable }
func (*SQLiteSink) Append(Event)                {}
func (*SQLiteSink) Close() error                { return nil }

func sqliteAvailable() bool { return false }
