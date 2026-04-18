package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/joshc/orchid"
)

func main() {
	setupFiles()

	fmt.Println("=== Simple: Ask a question with tools ===")
	fmt.Println()
	simple()

	fmt.Println()
	fmt.Println("=== Multi-step: Conversation with memory ===")
	fmt.Println()
	conversation()

	fmt.Println()
	fmt.Println("=== Fan-out: Parallel analysis ===")
	fmt.Println()
	fanOut()

	fmt.Println()
	fmt.Println("=== Power user: Policies + event log ===")
	fmt.Println()
	powerUser()

	fmt.Println()
	fmt.Println("=== Orchestration: Plan and implement a store ===")
	fmt.Println()
	storeOrchestration()
}

const endpoint = "http://localhost:8080"
const model = "gemma-4-e4b-it-Q4_K_M.gguf"

func simple() {
	a := orchid.New(endpoint, model)
	a.Can(orchid.Tool{Name: "read", Description: "Read a file given a path", Handler: readHandler})
	a.System("You are a helpful assistant. Be concise.")

	result, _, _ := a.Run("Read /tmp/orchid-api/notes.txt and summarize it in one sentence.")
	fmt.Println(result)
	fmt.Println()
	orchid.PrintLog(os.Stdout, a.Log())
}

func conversation() {
	a := orchid.New(endpoint, model)
	a.Can(orchid.Tool{Name: "bash", Description: "Run a shell command", Handler: bashHandler})
	a.System("You are a sysadmin assistant. Be concise.")

	r1, _, _ := a.Run("How many files are in /tmp/orchid-api/?")
	fmt.Println("Q1:", r1)

	r2, _, _ := a.Run("What are their names?")
	fmt.Println("Q2:", r2)
	fmt.Println()
	orchid.PrintLog(os.Stdout, a.Log())
}

func fanOut() {
	a := orchid.New(endpoint, model)
	a.Can(orchid.Tool{Name: "read", Description: "Read a file given a path", Handler: readHandler})
	a.System("Read the file and describe its format in one sentence.")

	results, _ := a.Fork([]string{
		"Read and describe /tmp/orchid-api/notes.txt",
		"Read and describe /tmp/orchid-api/data.csv",
		"Read and describe /tmp/orchid-api/config.json",
	}).Join()

	for i, r := range results {
		fmt.Printf("  Worker %d: %s\n", i, truncate(r, 120))
	}
	fmt.Println()
	orchid.PrintLog(os.Stdout, a.Log())
}

func powerUser() {
	a := orchid.New(endpoint, model)
	a.Can(
		orchid.Tool{Name: "read", Description: "Read a file given a path", Handler: readHandler},
		orchid.Tool{Name: "bash", Description: "Run a shell command", Handler: bashHandler},
	)
	a.System("You do exactly what the user asks. Be concise.")

	a.Rt().Effects.AddPolicy(func(p *orchid.Process, e *orchid.Effect) (bool, string) {
		if e.Tool == "bash" && strings.Contains(e.Args["command"], "rm ") {
			return false, "destructive commands blocked"
		}
		return true, ""
	})

	result, _, _ := a.Run("Run: rm -rf /tmp/orchid-api/")
	fmt.Println(result)
	fmt.Println()
	orchid.PrintLog(os.Stdout, a.Log())
}

func setupFiles() {
	os.MkdirAll("/tmp/orchid-api", 0o755)
	os.WriteFile("/tmp/orchid-api/notes.txt", []byte("TODO: implement caching\nBUG: timeout on large payloads\nDONE: auth middleware"), 0o644)
	os.WriteFile("/tmp/orchid-api/data.csv", []byte("name,age,city\nalice,30,portland\nbob,25,seattle"), 0o644)
	os.WriteFile("/tmp/orchid-api/config.json", []byte(`{"version": "1.0", "debug": true}`), 0o644)
}

var readHandler = func(args map[string]string) (string, error) {
	data, err := os.ReadFile(args["path"])
	if err != nil {
		return "", err
	}
	return string(data), nil
}

var bashHandler = func(args map[string]string) (string, error) {
	out, err := exec.Command("bash", "-c", args["command"]).CombinedOutput()
	if err != nil {
		return string(out) + "\n" + err.Error(), nil
	}
	return string(out), nil
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func storeOrchestration() {
	planner := orchid.New(endpoint, model)
	planner.System("You are a Go architect. When asked to design something, respond with ONLY a comma-separated list of function names needed. Nothing else. Example: Get,Set,Delete")

	plan, _, _ := planner.Run("design a key-value store")
	fmt.Println("Plan:", plan)

	functions := strings.Split(plan, ",")
	for i := range functions {
		functions[i] = strings.TrimSpace(functions[i])
	}

	implementer := orchid.New(endpoint, model)
	implementer.System("You are a Go developer. When asked to implement a function, respond with ONLY the method body. No package declaration, no struct definition, no imports, no explanation, no markdown fences.")

	tasks := make([]string, len(functions))
	for i, fn := range functions {
		tasks[i] = fmt.Sprintf("Implement the %s method for a Go key-value store type KVStore struct { data map[string]string }. Respond with ONLY the method, nothing else.", fn)
	}

	results, _ := implementer.Fork(tasks).Join()

	for i, r := range results {
		fmt.Printf("\n--- %s ---\n%s\n", functions[i], r)
	}

	var parts strings.Builder
	for i, r := range results {
		parts.WriteString(fmt.Sprintf("// %s\n%s\n\n", functions[i], r))
	}

	integrator := orchid.New(endpoint, model)
	integrator.System("You are a Go developer. You receive method implementations and assemble them into a single valid Go source file. Output ONLY valid Go code. No markdown fences, no explanation.")

	assembled, _, _ := integrator.Run(fmt.Sprintf(
		"Assemble these methods into a complete Go file with package store, the KVStore struct with field data map[string]string, a New() constructor, and all methods below:\n\n%s",
		parts.String()))

	fmt.Println("\n--- Assembled ---")
	fmt.Println(assembled)

	outPath := "/tmp/orchid-api/store.go"
	os.MkdirAll("/tmp/orchid-api", 0o755)
	os.WriteFile(outPath, []byte(assembled), 0o644)
	fmt.Printf("\nWrote %s\n", outPath)

	fmt.Println()
	orchid.PrintLog(os.Stdout, integrator.Log())
}
