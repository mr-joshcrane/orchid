package main

import (
	"fmt"
	"os"

	"github.com/joshc/orchid"
)

func main() {
	model := os.Getenv("BEDROCK_MODEL")
	if model == "" {
		fmt.Fprintln(os.Stderr, "set BEDROCK_MODEL to a Bedrock model ID or inference profile ARN")
		os.Exit(1)
	}

	gen, err := orchid.NewBedrockGenerator(model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create generator: %v\n", err)
		os.Exit(1)
	}

	a := orchid.NewFromGenerator(gen)
	a.System("You are a helpful assistant. Be concise.")

	fmt.Println("=== Simple question ===")
	result, _, err := a.Run("What is 2 + 2? Answer in one word.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)

	fmt.Println("\n=== With tools ===")
	b := orchid.NewFromGenerator(gen)
	b.Can(orchid.Tool{
		Name:        "read",
		Description: "Read a file given a path argument",
		Handler: func(args map[string]string) (string, error) {
			return "TODO: implement caching\nBUG: timeout on large payloads\nDONE: auth middleware", nil
		},
	})
	b.System("You are a helpful assistant. Be concise.")

	result, effects, err := b.Run("Read the file at /tmp/notes.txt and summarize it.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
	for _, e := range effects {
		fmt.Printf("  effect: tool=%s admitted=%v result=%q err=%q\n", e.Tool, e.Admitted, e.Result, e.Error)
	}

	fmt.Println("\n=== Event log ===")
	orchid.PrintLog(os.Stdout, b.Log())
}
