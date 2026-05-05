package orchid

import (
	"context"
	"fmt"
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
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d content", i))
	}
	content := strings.Join(lines, "\n") + "\n"
	writeTestFile(t, dir, "big.txt", content)

	tool := ReadTool(dir, ReadWithMaxBytes(5000), ReadWithBinaryDetect(false))

	t.Run("full file within limit", func(t *testing.T) {
		small := strings.Join(lines[:10], "\n") + "\n"
		writeTestFile(t, dir, "small.txt", small)
		smallTool := ReadTool(dir, ReadWithMaxBytes(5000), ReadWithBinaryDetect(false))
		result, _, err := callContextualHandler(t, smallTool, map[string]string{"path": "small.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if result != small {
			t.Fatalf("expected full content, got %d bytes", len(result))
		}
	})

	t.Run("line_start reads from line", func(t *testing.T) {
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "line_start": "50"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(result, "line 50 content\n") {
			t.Fatalf("expected to start at line 50, got: %s", result[:40])
		}
	})

	t.Run("oversized file returns first page with hint", func(t *testing.T) {
		small := ReadTool(dir, ReadWithMaxBytes(200), ReadWithBinaryDetect(false))
		result, _, err := callContextualHandler(t, small, map[string]string{"path": "big.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result, "[FILE TRUNCATED:") {
			t.Fatalf("expected truncation hint, got: %s", result)
		}
		if !strings.Contains(result, "line_start") {
			t.Fatalf("expected line_start hint, got: %s", result)
		}
	})

	t.Run("negative line_start rejected", func(t *testing.T) {
		_, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "line_start": "-1"})
		if err == nil {
			t.Fatal("expected error for negative line_start")
		}
	})

	t.Run("line_start beyond file returns empty", func(t *testing.T) {
		result, _, err := callContextualHandler(t, tool, map[string]string{"path": "big.txt", "line_start": "9999"})
		if err != nil {
			t.Fatal(err)
		}
		if result != "" {
			t.Fatalf("expected empty result for line_start beyond file, got %q", result)
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
		result, _, err := callContextualHandler(t, tool, map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) > 8_000 {
			t.Fatalf("result length = %d, exceeds default 8000 byte cap", len(result))
		}
	})

	t.Run("small byte cap truncates entries", func(t *testing.T) {
		tool := ListTool(dir, ListWithMaxBytes(100))
		result, _, err := callContextualHandler(t, tool, map[string]string{})
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
		result, _, err := callContextualHandler(t, tool, map[string]string{})
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
		result, _, err := callContextualHandler(t, tool, map[string]string{"pattern": "findme"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) > 16_000 {
			t.Fatalf("result length = %d, exceeds default 16000 byte cap", len(result))
		}
	})

	t.Run("small byte cap truncates matches", func(t *testing.T) {
		tool := SearchTool(dir, SearchWithMaxBytes(200))
		result, _, err := callContextualHandler(t, tool, map[string]string{"pattern": "findme"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) > 200 {
			t.Fatalf("result length = %d, exceeds 200 byte cap", len(result))
		}
	})

	t.Run("match cap limits count", func(t *testing.T) {
		tool := SearchTool(dir, SearchWithMaxMatches(3))
		result, _, err := callContextualHandler(t, tool, map[string]string{"pattern": "findme"})
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
	if !strings.Contains(tool.Description, "line_start") {
		t.Fatalf("default description should mention line_start, got: %s", tool.Description)
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

func TestWithRootDirRejectsNonExistent(t *testing.T) {
	gen := GeneratorFunc(func(ctx Context) Proposal { return Proposal{Text: "done"} })
	_, err := NewWorkflowWithOptions(gen, WithRootDir("/nonexistent/path/xyz"))
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

func TestWithRootDirResolvesAbsolute(t *testing.T) {
	dir := t.TempDir()
	gen := GeneratorFunc(func(ctx Context) Proposal { return Proposal{Text: "done"} })
	wf, err := NewWorkflowWithOptions(gen, WithRootDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if wf.rootDir == "" {
		t.Fatal("rootDir should be set")
	}
	if wf.rootDir != dir {
		t.Fatalf("rootDir = %q, want %q", wf.rootDir, dir)
	}
}

func TestRootDirPropagatedToHandler(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "hello.txt", "world")

	var gotRootDir string
	tool := Tool{
		Name: "check",
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			gotRootDir = hctx.RootDir
			return "ok", nil
		},
	}

	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text:    "checking",
			Effects: []ProposedEffect{{Tool: "check", Args: map[string]string{}}},
		}
	})

	wf, err := NewWorkflowWithOptions(gen, WithRootDir(dir), WithTools(tool))
	if err != nil {
		t.Fatal(err)
	}
	wf.Run(context.Background(), "test", "go")
	if gotRootDir != dir {
		t.Fatalf("RootDir in handler = %q, want %q", gotRootDir, dir)
	}
}

func TestRootDirOverridesToolConstructorRoot(t *testing.T) {
	constructorDir := t.TempDir()
	workflowDir := t.TempDir()
	writeTestFile(t, workflowDir, "fromworkflow.txt", "yes")

	tool := ReadTool(constructorDir)
	gen := GeneratorFunc(func(ctx Context) Proposal {
		if len(ctx.ToolResults) > 0 {
			return Proposal{Text: "done"}
		}
		return Proposal{
			Text:    "reading",
			Effects: []ProposedEffect{{Tool: "read", Args: map[string]string{"path": "fromworkflow.txt"}}},
		}
	})

	wf, err := NewWorkflowWithOptions(gen, WithRootDir(workflowDir), WithTools(tool))
	if err != nil {
		t.Fatal(err)
	}
	text, _, runErr := wf.Run(context.Background(), "test", "go")
	if runErr != nil {
		t.Fatal(runErr)
	}
	if text != "done" {
		t.Fatalf("text = %q, want done (file should be found via workflow rootDir)", text)
	}
}
