//go:build sqlite

package orchid

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLiteSink_RoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sink, err := newSQLiteSink(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Append one event per major type with realistic Data.
	events := []Event{
		{Type: EventProcessSpawned, ProcessID: "proc_1", Data: map[string]string{"name": "test", "purpose": "testing", "parent": ""}},
		{Type: EventUserInput, ProcessID: "proc_1", Data: map[string]string{"content": "hello"}},
		{Type: EventAssistantOutput, ProcessID: "proc_1", Data: map[string]string{"content": "world", "input_tokens": "100", "output_tokens": "25", "model": "test-model"}},
		{Type: EventEffectIntent, ProcessID: "proc_1", Data: map[string]string{"tool": "read", "effect_id": "eff_1", "path": "/tmp/foo"}},
		{Type: EventEffectObserved, ProcessID: "proc_1", Data: map[string]string{"tool": "read", "effect_id": "eff_1", "result": "file contents", "duration_ms": "42"}},
		{Type: EventEffectRejected, ProcessID: "proc_1", Data: map[string]string{"effect_id": "eff_2", "reason": "no permission"}},
		{Type: EventPromptSize, ProcessID: "proc_1", Data: map[string]string{"estimated_tokens": "5000", "model_limit": "8000", "utilisation_pct": "62"}},
	}

	now := time.Now()
	for i, e := range events {
		e.ID = uint64(i)
		e.Timestamp = now.Add(time.Duration(i) * time.Second)
		sink.Append(e)
	}

	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen read-only and verify.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(events) {
		t.Fatalf("count = %d, want %d", count, len(events))
	}

	// Check denormalized columns on the EffectObserved row.
	var tool sql.NullString
	var durMs sql.NullInt64
	if err := db.QueryRow("SELECT tool, duration_ms FROM events WHERE type = 'EffectObserved'").Scan(&tool, &durMs); err != nil {
		t.Fatal(err)
	}
	if !tool.Valid || tool.String != "read" {
		t.Fatalf("tool = %v, want 'read'", tool)
	}
	if !durMs.Valid || durMs.Int64 != 42 {
		t.Fatalf("duration_ms = %v, want 42", durMs)
	}

	// Check tokens on AssistantOutput.
	var tokIn, tokOut sql.NullInt64
	if err := db.QueryRow("SELECT tokens_in, tokens_out FROM events WHERE type = 'AssistantOutput'").Scan(&tokIn, &tokOut); err != nil {
		t.Fatal(err)
	}
	if !tokIn.Valid || tokIn.Int64 != 100 {
		t.Fatalf("tokens_in = %v, want 100", tokIn)
	}
	if !tokOut.Valid || tokOut.Int64 != 25 {
		t.Fatalf("tokens_out = %v, want 25", tokOut)
	}

	// Check NULLs on UserInput (no tool, no tokens).
	var uTool sql.NullString
	var uTokIn sql.NullInt64
	if err := db.QueryRow("SELECT tool, tokens_in FROM events WHERE type = 'UserInput'").Scan(&uTool, &uTokIn); err != nil {
		t.Fatal(err)
	}
	if uTool.Valid {
		t.Fatalf("UserInput tool should be NULL, got %v", uTool)
	}
	if uTokIn.Valid {
		t.Fatalf("UserInput tokens_in should be NULL, got %v", uTokIn)
	}
}

func TestSQLiteSink_JsonExtract(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sink, err := newSQLiteSink(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	e := Event{
		ID:        0,
		Type:      EventEffectIntent,
		Timestamp: time.Now(),
		ProcessID: "proc_1",
		Data:      map[string]string{"tool": "semantic_search", "effect_id": "eff_1", "query": "foo bar"},
	}
	sink.Append(e)
	sink.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var query string
	err = db.QueryRow("SELECT json_extract(data, '$.query') FROM events WHERE type = 'EffectIntent'").Scan(&query)
	if err != nil {
		t.Fatal(err)
	}
	if query != "foo bar" {
		t.Fatalf("json_extract query = %q, want %q", query, "foo bar")
	}
}

func TestWithSQLiteLog_Close(t *testing.T) {
	logDir := t.TempDir()
	dbPath := filepath.Join(logDir, "events.db")

	gen := GeneratorFunc(func(ctx Context) Proposal {
		return Proposal{Text: "done", InputTokens: 10, OutputTokens: 5}
	})

	readTool := Tool{
		Name:    "read",
		Handler: func(args map[string]string) (string, error) { return "data", nil },
	}

	wf, err := NewWorkflowWithOptions(gen, WithTools(readTool), WithSQLiteLog(dbPath))
	if err != nil {
		t.Fatal(err)
	}

	_, _, runErr := wf.Run(context.Background(), "test", "go")
	if runErr != nil {
		t.Fatal(runErr)
	}

	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify the DB has rows.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("events.db should have >0 rows after a workflow run")
	}
}
