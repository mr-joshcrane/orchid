package orchid

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func callContextualHandler(t *testing.T, tool Tool, args map[string]string) (string, []Event, error) {
	t.Helper()
	var events []Event
	hctx := HandlerContext{
		Ctx:       context.Background(),
		ProcessID: "test_proc",
		ToolName:  tool.Name,
		LogAppend: func(e Event) { events = append(events, e) },
	}
	result, err := tool.ContextualHandler(hctx, args)
	return result, events, err
}

func TestReadToolPagination(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("ABCDEFGHIJ", 10)
	writeTestFile(t, dir, "big.txt", content)

	tool := ReadTool(dir, ReadWithMaxBytes(100), ReadWithBinaryDetect(false))

	t.Run("full file within limit", func(t *testing.T) {
		result, events, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if result != content {
			t.Fatalf("expected full content, got %d bytes", len(result))
		}
		if len(events) != 0 {
			t.Fatalf("expected no truncation events, got %d", len(events))
		}
	})

	t.Run("offset reads from position", func(t *testing.T) {
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "offset": "90"})
		if err != nil {
			t.Fatal(err)
		}
		if result != "ABCDEFGHIJ" {
			t.Fatalf("result = %q, want last 10 bytes", result)
		}
	})

	t.Run("offset + length", func(t *testing.T) {
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "offset": "10", "length": "10"})
		if err != nil {
			t.Fatal(err)
		}
		if result != "ABCDEFGHIJ" {
			t.Fatalf("result = %q, want 10 bytes from offset 10", result)
		}
	})

	t.Run("truncation emits event", func(t *testing.T) {
		small := ReadTool(dir, ReadWithMaxBytes(20), ReadWithBinaryDetect(false))
		result, events, err := callContextualHandler(t, small, map[string]string{"path": "big.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 20 {
			t.Fatalf("result length = %d, want 20", len(result))
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 truncation event, got %d", len(events))
		}
		if events[0].Type != EventOutputTruncated {
			t.Fatalf("event type = %v, want EventOutputTruncated", events[0].Type)
		}
	})

	t.Run("negative offset rejected", func(t *testing.T) {
		_, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "offset": "-1"})
		if err == nil {
			t.Fatal("expected error for negative offset")
		}
	})

	t.Run("negative length rejected", func(t *testing.T) {
		_, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "length": "-5"})
		if err == nil {
			t.Fatal("expected error for negative length")
		}
	})

	t.Run("offset beyond file returns empty", func(t *testing.T) {
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "offset": "9999"})
		if err != nil {
			t.Fatal(err)
		}
		if result != "" {
			t.Fatalf("expected empty result for offset beyond file, got %q", result)
		}
	})

	t.Run("MaxBytes 0 disables cap", func(t *testing.T) {
		uncapped := ReadTool(dir, ReadWithMaxBytes(0), ReadWithBinaryDetect(false))
		result, events, err := callContextualHandler(t, uncapped, map[string]string{"path": "big.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if result != content {
			t.Fatalf("expected full content with MaxBytes(0), got %d bytes", len(result))
		}
		if len(events) != 0 {
			t.Fatalf("expected no truncation events, got %d", len(events))
		}
	})
}

func TestReadToolBinaryRefusal(t *testing.T) {
	dir := t.TempDir()

	binaryData := make([]byte, 100)
	for i := range binaryData {
		binaryData[i] = byte(i % 16)
	}
	writeTestFile(t, dir, "binary.dat", string(binaryData))
	writeTestFile(t, dir, "text.txt", "hello world\n")

	t.Run("binary file rejected", func(t *testing.T) {
		tool := ReadTool(dir)
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "binary.dat"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(result, "TOOL_REJECTED:") {
			t.Fatalf("expected TOOL_REJECTED, got %q", result)
		}
	})

	t.Run("text file accepted", func(t *testing.T) {
		tool := ReadTool(dir)
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "text.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if result != "hello world\n" {
			t.Fatalf("result = %q", result)
		}
	})

	t.Run("binary detect disabled allows binary", func(t *testing.T) {
		tool := ReadTool(dir, ReadWithBinaryDetect(false))
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "binary.dat"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(result, "TOOL_REJECTED:") {
			t.Fatal("should not reject when binary detect is disabled")
		}
	})
}

func TestListToolByteCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		writeTestFile(t, dir, filepath.Join("sub", strings.Repeat("x", 20)+string(rune('a'+i%26))+".go"), "package x")
	}

	t.Run("default cap limits output", func(t *testing.T) {
		tool := ListTool(dir)
		result, err := tool.Handler(map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) > 8_000 {
			t.Fatalf("result length = %d, exceeds default 8000 byte cap", len(result))
		}
	})

	t.Run("small byte cap truncates entries", func(t *testing.T) {
		tool := ListTool(dir, ListWithMaxBytes(100))
		result, err := tool.Handler(map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) > 100 {
			t.Fatalf("result length = %d, exceeds 100 byte cap", len(result))
		}
		lines := strings.Split(result, "\n")
		if len(lines) >= 50 {
			t.Fatalf("expected fewer than 50 entries under byte cap, got %d", len(lines))
		}
	})

	t.Run("entry cap limits count", func(t *testing.T) {
		tool := ListTool(dir, ListWithMaxEntries(5))
		result, err := tool.Handler(map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(result, "\n")
		if len(lines) > 5 {
			t.Fatalf("expected <= 5 entries, got %d", len(lines))
		}
	})
}

func TestSearchToolByteCap(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "findme " + strings.Repeat("x", 50)
	}
	writeTestFile(t, dir, "big.go", strings.Join(lines, "\n"))

	t.Run("default cap limits output", func(t *testing.T) {
		tool := SearchTool(dir)
		result, err := tool.Handler(map[string]string{"pattern": "findme"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) > 16_000 {
			t.Fatalf("result length = %d, exceeds default 16000 byte cap", len(result))
		}
	})

	t.Run("small byte cap truncates matches", func(t *testing.T) {
		tool := SearchTool(dir, SearchWithMaxBytes(200))
		result, err := tool.Handler(map[string]string{"pattern": "findme"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) > 200 {
			t.Fatalf("result length = %d, exceeds 200 byte cap", len(result))
		}
	})

	t.Run("match cap limits count", func(t *testing.T) {
		tool := SearchTool(dir, SearchWithMaxMatches(3))
		result, err := tool.Handler(map[string]string{"pattern": "findme"})
		if err != nil {
			t.Fatal(err)
		}
		matchLines := strings.Split(result, "\n")
		if len(matchLines) > 3 {
			t.Fatalf("expected <= 3 matches, got %d", len(matchLines))
		}
	})
}

func TestReadToolDescription(t *testing.T) {
	tool := ReadTool("/tmp/test")
	if !strings.Contains(tool.Description, "32000") {
		t.Fatalf("default description should mention 32000 bytes, got: %s", tool.Description)
	}
	if !strings.Contains(tool.Description, "paginate") {
		t.Fatalf("default description should mention pagination, got: %s", tool.Description)
	}

	custom := ReadTool("/tmp/test", ReadWithMaxBytes(1000))
	if !strings.Contains(custom.Description, "1000") {
		t.Fatalf("custom description should mention 1000 bytes, got: %s", custom.Description)
	}
}

func TestReadToolDirectoryListing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.txt", "aaa")
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	tool := ReadTool(dir)
	result, _, err := callContextualHandler(t, tool, map[string]string{"path": "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "a.txt") {
		t.Fatalf("directory listing should contain a.txt, got: %s", result)
	}
	if !strings.Contains(result, "subdir/") {
		t.Fatalf("directory listing should contain subdir/, got: %s", result)
	}
}

func TestReadToolPathEscape(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "safe.txt", "ok")

	tool := ReadTool(dir)
	_, _, err := callContextualHandler(t, tool, map[string]string{"path": "../../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path escape")
	}
}
