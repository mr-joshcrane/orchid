package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joshc/orchid"
)

const endpoint = "http://localhost:8080"
const model = "gemma-4-e4b-it-Q4_K_M.gguf"
const targetPkg = "messytodo"
const maxFixes = 3

func main() {
	code := readFile("todo.go")
	fmt.Printf("Original: %d lines\n\n", len(strings.Split(code, "\n")))

	fmt.Println("=== Phase 1: Generate tests (fan-out) ===")
	testFuncs := generateTests(code)

	fmt.Println("\n=== Phase 2: Assemble and compile tests ===")
	tests := assembleTests(testFuncs)
	writeFile("todo_test.go", tests)
	tests = compileTestLoop(code, tests)
	if compileCheck() != "" {
		fmt.Println("Could not get tests to compile. Aborting.")
		return
	}

	fmt.Println("\n=== Phase 3: Run tests ===")
	failures := runTests()
	if failures == "" {
		fmt.Println("All tests pass. No bugs found.")
		return
	}

	var failLines []string
	for _, line := range strings.Split(failures, "\n") {
		if strings.Contains(line, "FAIL") || strings.Contains(line, "--- FAIL") || strings.Contains(line, "got") || strings.Contains(line, "expected") || strings.Contains(line, "Error") {
			failLines = append(failLines, line)
		}
	}
	failSummary := strings.Join(failLines, "\n")
	fmt.Println(failSummary)

	fmt.Println("\n=== Phase 4: Fix code ===")
	fixed := fixFromFailures(code, tests, failSummary)
	writeFile("todo.go", fixed)

	fmt.Println("\n=== Phase 5: Compile ===")
	fixed = compileCodeLoop(fixed)

	fmt.Println("\n=== Phase 6: Re-run tests ===")
	rerun := runTests()
	if rerun == "" {
		fmt.Println("All tests pass!")
	} else {
		var rerunFails []string
		for _, line := range strings.Split(rerun, "\n") {
			if strings.Contains(line, "FAIL") || strings.Contains(line, "--- FAIL") {
				rerunFails = append(rerunFails, line)
			}
		}
		fmt.Printf("Still failing:\n%s\n", strings.Join(rerunFails, "\n"))
	}

	fmt.Printf("\nFinal: %d lines\n", len(strings.Split(fixed, "\n")))
}

func generateTests(code string) []string {
	methods := []string{
		"Add and Get",
		"Complete and Reopen",
		"Delete",
		"SetDueDate and Overdue",
		"ByPriority and ByTag",
		"Search",
		"Stats and Pending and Completed",
		"Sorted",
		"AddTag and RemoveTag",
		"Rename and SetPriority",
		"ClearCompleted",
	}

	a := orchid.New(endpoint, model)
	a.System(`You are a Go test engineer. Write a single test function for the specified methods. Cover normal usage and edge cases including: zero time values, completed vs pending items in filter functions, duplicate tags, case sensitivity in search. Output ONLY a single func TestXxx(t *testing.T) { ... } with NO package, NO imports, NO markdown. Use t.Errorf for failures.`)

	tasks := make([]string, len(methods))
	for i, m := range methods {
		tasks[i] = fmt.Sprintf("Write a test function for the %s method(s):\n\n%s", m, code)
	}

	results, _ := a.Fork(tasks).Join()

	for i, r := range results {
		fmt.Printf("  %s: %d lines\n", methods[i], len(strings.Split(r, "\n")))
	}

	return results
}

func assembleTests(funcs []string) string {
	var buf strings.Builder
	buf.WriteString("package messytodo\n\nimport (\n\t\"strings\"\n\t\"testing\"\n\t\"time\"\n)\n\n")

	for _, f := range funcs {
		cleaned := strings.TrimSpace(f)
		cleaned = strings.TrimPrefix(cleaned, "```go")
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
		buf.WriteString(cleaned)
		buf.WriteString("\n\n")
	}

	// suppress unused import errors
	buf.WriteString("var _ = strings.Contains\nvar _ = time.Now\n")
	return buf.String()
}

func compileTestLoop(code string, tests string) string {
	original := tests
	for attempt := 0; attempt < maxFixes; attempt++ {
		errors := compileCheck()
		if errors == "" {
			fmt.Println("Tests compile!")
			return tests
		}
		fmt.Printf("\nTest compile errors (attempt %d/%d):\n%s\n", attempt+1, maxFixes, errors)

		if attempt == maxFixes-1 {
			fmt.Println("Gave up compiling tests. Reverting.")
			tests = original
			writeFile("todo_test.go", tests)
			return tests
		}

		a := orchid.New(endpoint, model)
		a.System("You are a Go developer fixing test compile errors. Output the complete fixed test file. No markdown, no explanation.")
		fixed, _, _ := a.Run(fmt.Sprintf("Fix these compile errors in the test file:\n\n%s\n\nTEST CODE:\n%s\n\nSOURCE CODE UNDER TEST:\n%s", errors, tests, code))
		tests = fixed
		writeFile("todo_test.go", tests)
	}
	return tests
}

func runTests() string {
	base, _ := os.Getwd()
	cmd := exec.Command("go", "test", "-v", "-count=1", "./"+targetPkg+"/")
	cmd.Dir = base
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func fixFromFailures(code string, tests string, failures string) string {
	a := orchid.New(endpoint, model)
	a.System(`You are a Go developer fixing bugs revealed by failing tests. The tests are correct and the code is wrong. Fix ONLY the source code so all tests pass. Output the complete fixed source file. No markdown, no explanation.`)

	result, _, _ := a.Run(fmt.Sprintf("These test failures indicate bugs in the source code:\n\n%s\n\nSOURCE CODE TO FIX:\n%s", failures, code))
	return result
}

func compileCodeLoop(code string) string {
	original := code
	for attempt := 0; attempt < maxFixes; attempt++ {
		errors := compileCheck()
		if errors == "" {
			fmt.Println("Compiles clean!")
			return code
		}
		fmt.Printf("\nCompile errors (attempt %d/%d):\n%s\n", attempt+1, maxFixes, errors)

		if attempt == maxFixes-1 {
			fmt.Println("Gave up. Reverting.")
			code = original
			writeFile("todo.go", code)
			return code
		}

		a := orchid.New(endpoint, model)
		a.System("You are a Go developer fixing compile errors. Output the complete fixed file. No markdown, no explanation.")
		fixed, _, _ := a.Run(fmt.Sprintf("Fix these compile errors:\n\n%s\n\nCODE:\n%s", errors, code))
		code = fixed
		writeFile("todo.go", code)
	}
	return code
}

func readFile(name string) string {
	base, _ := os.Getwd()
	data, _ := os.ReadFile(filepath.Join(base, targetPkg, name))
	return string(data)
}

func writeFile(name string, content string) {
	base, _ := os.Getwd()
	path := filepath.Join(base, targetPkg, name)
	os.WriteFile(path, []byte(content), 0o644)
	fmt.Printf("Wrote %s\n", path)
}

func compileCheck() string {
	base, _ := os.Getwd()
	cmd := exec.Command("go", "test", "-c", "-o", "/dev/null", "./"+targetPkg+"/")
	cmd.Dir = base
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}
