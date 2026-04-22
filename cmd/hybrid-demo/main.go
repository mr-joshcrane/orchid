package main

import (
	"fmt"
	"os"

	"github.com/mr-joshcrane/orchid"
)

const localEndpoint = "https://nixcariot.tail60ffe.ts.net"
const localModel = "gemma-4-e4b-it-Q4_K_M.gguf"

func main() {
	bedrockModel := os.Getenv("BEDROCK_MODEL")
	if bedrockModel == "" {
		fmt.Fprintln(os.Stderr, "set BEDROCK_MODEL to a Bedrock model ID or inference profile ARN")
		os.Exit(1)
	}

	gen, err := orchid.NewBedrockGenerator(bedrockModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bedrock: %v\n", err)
		os.Exit(1)
	}

	director := orchid.New(gen)
	director.System("You are a senior engineer directing junior engineers. Break tasks into clear subtasks. For each subtask, output ONLY the instruction on its own line prefixed with TASK: — no commentary.")

	director.GiveTask("I need three short Go utility functions: one that reverses a string, one that checks if a number is prime, and one that finds the max in a slice of ints. Give me a TASK: line for each.")
	plan, _, err := director.Step()
	if err != nil {
		fmt.Fprintf(os.Stderr, "director error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== Director (Bedrock) plan ===")
	fmt.Println(plan)
	fmt.Println()

	var tasks []string
	for _, line := range splitLines(plan) {
		if len(line) > 6 && line[:6] == "TASK: " {
			tasks = append(tasks, line[6:])
		}
	}

	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "director produced no TASK: lines")
		os.Exit(1)
	}

	fmt.Printf("=== Forking %d local workers (llama @ %s) ===\n\n", len(tasks), localEndpoint)

	local := orchid.NewLocalAgent(localEndpoint, localModel)
	local.System("You are a Go developer. Output ONLY the Go function. No explanation, no markdown fences.")

	results, err := local.Fork(tasks...).Join()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fork error: %v\n", err)
		os.Exit(1)
	}

	for i, r := range results {
		fmt.Printf("--- Worker %d ---\n%s\n\n", i, r.Text)
	}

	fmt.Println("=== Process tree ===")
	orchid.PrintTree(os.Stdout, orchid.BuildProcessTree(local.Log()))
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
