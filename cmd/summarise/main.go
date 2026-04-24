package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mr-joshcrane/orchid"
)

func main() {
	gen := orchid.NewOpenAIGenerator("http://localhost:8080/v1")

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: summarise <dir>")
		os.Exit(1)
	}
	root := os.Args[1]

	wf := orchid.NewWorkflow(gen,
		orchid.SearchTool(root),
	)
	wf.Log.SetSink(os.Stderr)

	text, usage, err := wf.Run(context.Background(),
		"You are a Go code analyst with access to a search tool that searches file contents by regex. Use it to explore the codebase. Do not ask for input — start searching immediately.",
		"Analyse this Go project and summarise its architecture, key types, and how the pieces fit together. Start by searching for struct and interface definitions.",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(text)
	fmt.Fprintf(os.Stderr, "\ntokens: %d in / %d out\n", usage.InputTokens, usage.OutputTokens)
}
