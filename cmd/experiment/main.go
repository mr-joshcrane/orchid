package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mr-joshcrane/orchid"
)

// ── Semantic index types (mirrors cmd/semantic) ──

type Chunk struct {
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	File      string    `json:"file"`
	LineStart int       `json:"line_start"`
	LineEnd   int       `json:"line_end"`
	Source    string    `json:"source"`
	Embedding []float64 `json:"embedding"`
}

type Index struct {
	Endpoint string  `json:"endpoint"`
	Chunks   []Chunk `json:"chunks"`
}

func loadIndex(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	d := math.Sqrt(na) * math.Sqrt(nb)
	if d == 0 {
		return 0
	}
	return dot / d
}

func embedQuery(query string) ([]float64, error) {
	embBin := os.Getenv("LLAMA_EMBEDDING")
	if embBin == "" {
		return nil, fmt.Errorf("LLAMA_EMBEDDING not set")
	}
	embModel := os.Getenv("EMBED_MODEL")
	if embModel == "" {
		return nil, fmt.Errorf("EMBED_MODEL not set")
	}
	cmd := exec.Command(embBin,
		"-m", embModel,
		"--pooling", "last",
		"-c", "4096",
		"-ngl", "99",
		"--embd-output-format", "json",
		"-p", strings.ReplaceAll(query, "\n", " "),
		"--no-warmup",
	)
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH=/run/opengl-driver/lib:"+os.Getenv("LD_LIBRARY_PATH"))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("llama-embedding: %w", err)
	}
	var resp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return resp.Data[0].Embedding, nil
}

// SemanticSearchTool creates an Orchid tool that searches the pre-built index.
func SemanticSearchTool(idx *Index) orchid.Tool {
	return orchid.Tool{
		Name:        "semantic_search",
		Description: "Search the codebase by meaning. Given a natural-language query, returns the top 10 most semantically similar code chunks (functions, types, etc.) with file locations and similarity scores.",
		Params: map[string]orchid.Param{
			"query": {Type: "string", Description: "Natural language description of what you're looking for", Required: true},
		},
		Parallelizable: true,
		ContextualHandler: func(hctx orchid.HandlerContext, args map[string]string) (string, error) {
			query := args["query"]
			if query == "" {
				return "", fmt.Errorf("query is required")
			}

			qEmb, err := embedQuery(query)
			if err != nil {
				return "", fmt.Errorf("embedding query: %w", err)
			}

			type result struct {
				chunk Chunk
				score float64
			}
			var results []result
			for _, c := range idx.Chunks {
				results = append(results, result{chunk: c, score: cosine(qEmb, c.Embedding)})
			}
			sort.Slice(results, func(i, j int) bool {
				return results[i].score > results[j].score
			})

			n := 10
			if n > len(results) {
				n = len(results)
			}

			var sb strings.Builder
			for i := 0; i < n; i++ {
				r := results[i]
				fmt.Fprintf(&sb, "%2d. %.4f  %-8s %-40s %s:%d-%d\n",
					i+1, r.score, r.chunk.Kind, r.chunk.Name, r.chunk.File, r.chunk.LineStart, r.chunk.LineEnd)
			}
			return sb.String(), nil
		},
	}
}

// ── Questions ──

// loadQuestions returns questions from QUESTIONS_FILE (JSON array of strings) if set,
// otherwise falls back to hardcoded traefik questions.
func loadQuestions() []string {
	if qf := os.Getenv("QUESTIONS_FILE"); qf != "" {
		data, err := os.ReadFile(qf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read questions file: %v\n", err)
			os.Exit(1)
		}
		var qs []string
		if err := json.Unmarshal(data, &qs); err != nil {
			fmt.Fprintf(os.Stderr, "parse questions file: %v\n", err)
			os.Exit(1)
		}
		return qs
	}
	return defaultQuestions
}

var defaultQuestions = []string{
	"Find the exact function that checks whether a TLS certificate is about to expire and triggers renewal. Give me the file path, function name, and the expiration threshold used.",
	"Find the function that converts Docker container labels into Traefik route configuration. What file is it in, what is it called, and what are its parameters?",
	"Find the code that implements the token bucket for the rate limiting middleware. What struct holds the state, and what function consumes tokens?",
	"Find the function that matches an incoming HTTP request to a routing rule (like Host, Path, etc). What file is it in and how does the matching work?",
	"Find the code that handles draining in-flight connections during graceful shutdown. What function does this, and what timeout is used?",
	"Find the health check implementation — specifically the function that sends a probe to a backend server and marks it up or down. What file and function?",
	"Find where middleware chains are built from the dynamic configuration. What function takes middleware config and returns an http.Handler chain?",

	// ── Harder questions: require semantic understanding, not just keyword grep ──

	// Q7: The retry middleware uses exponential backoff — but what determines whether a failed request is retryable?
	"The retry middleware decides whether to retry a failed request or give up. Find the exact logic that determines if a response is retryable (not the backoff timing, but the decision of WHETHER to retry). What conditions cause it to retry vs. not retry? Give file paths and function names.",

	// Q8: P2C load balancing — how does it actually pick between two random servers?
	"Traefik has a power-of-two-choices (P2C) load balancer. Find the function that selects which backend server handles a request. How does it pick two candidates and decide between them? What metric does it use to compare? Give the file path and function name.",

	// Q9: How does Traefik swap to a new routing configuration without dropping existing connections?
	"When Traefik hot-reloads its configuration, it needs to swap in new routing rules without dropping in-flight connections. Find the mechanism that atomically switches the active router/handler. What function performs this swap, and how does it ensure existing connections are not interrupted? Give file paths and function names.",

	// Q10: ACME TLS-ALPN challenge — how does it inject the self-signed cert?
	"When Traefik solves an ACME TLS-ALPN-01 challenge, it needs to temporarily serve a special self-signed certificate on the challenged domain. Find the code that provisions this temporary certificate and makes it available to incoming TLS handshakes. What function creates it and where is it stored? Give file paths and function names.",

	// Q11: How does the access log middleware decide what to include in a log entry?
	"The access log middleware captures detailed information about each request/response (status, duration, headers, etc). Find the function that assembles the final log entry from the request and response data. What struct holds the log fields, and what function populates it? Give file paths.",

	// Q12: How does the file provider detect config changes and debounce rapid updates?
	"The file-based configuration provider watches for file changes and sends new configs. Find the code that debounces rapid file change events to avoid sending multiple config updates in quick succession. What mechanism is used (polling, fsnotify, timer)? Give file path and function name.",

	// Q13: How does the sticky session cookie get validated on subsequent requests?
	"When a client returns with a sticky session cookie, Traefik needs to look up the original backend server and verify it's still available. Find the function that reads the cookie, resolves it to a backend, and handles the case where that backend is no longer healthy. Give file path and function name.",
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: experiment <control|treatment|combined|count> [question-index]")
		os.Exit(1)
	}
	if os.Args[1] == "count" {
		qs := loadQuestions()
		fmt.Println(len(qs))
		return
	}
	mode := os.Args[1]

	root := os.Getenv("CODEBASE_ROOT")
	if root == "" {
		root = "/tmp/traefik"
	}

	codebaseName := os.Getenv("CODEBASE_NAME")
	if codebaseName == "" {
		codebaseName = filepath.Base(root)
	}

	// Base tools both conditions get
	baseTools := []orchid.WorkflowOption{
		orchid.WithTools(
			orchid.ReadTool(root),
			orchid.SearchTool(root),
			orchid.ListTool(root),
		),
		orchid.WithLogDir(fmt.Sprintf("/tmp/experiment-%s-%d", mode, time.Now().Unix())),
		orchid.WithRootDir(root),
		orchid.WithEffectConcurrency(4),
	}

	// Treatment and combined modes add the semantic search tool
	if mode == "treatment" || mode == "combined" {
		indexPath := os.Getenv("SEMANTIC_INDEX")
		if indexPath == "" {
			indexPath = filepath.Join(root, "index.json")
		}
		idx, err := loadIndex(indexPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load index: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "loaded semantic index with %d chunks\n", len(idx.Chunks))
		baseTools = append(baseTools, orchid.WithTools(SemanticSearchTool(idx)))
	}

	gen := orchid.NewOpenAIGenerator("http://localhost:8080/v1")

	// Select questions
	allQuestions := loadQuestions()
	selectedQuestions := allQuestions
	if len(os.Args) > 2 {
		var err error
		qIdx := 0
		fmt.Sscanf(os.Args[2], "%d", &qIdx)
		if err != nil || qIdx < 0 || qIdx >= len(allQuestions) {
			fmt.Fprintf(os.Stderr, "invalid question index (0-%d)\n", len(allQuestions)-1)
			os.Exit(1)
		}
		selectedQuestions = []string{allQuestions[qIdx]}
	}

	purpose := fmt.Sprintf(`You are a code analyst exploring the %s codebase.
Your job is to find specific code by using the tools available to you.
RULES:
- You MUST cite exact file paths, function names, and line numbers from the actual code.
- Do NOT answer from general knowledge. Every claim must come from code you've read with your tools.
- If you haven't seen it in the code, say you couldn't find it.
- Start searching immediately — do not ask for clarification.`, codebaseName)

	for i, q := range selectedQuestions {
		fmt.Fprintf(os.Stderr, "\n═══ Question %d/%d [%s] ═══\n%s\n\n", i+1, len(selectedQuestions), mode, q)

		wf, err := orchid.NewWorkflowWithOptions(gen, baseTools...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "workflow: %v\n", err)
			os.Exit(1)
		}

		start := time.Now()
		text, usage, err := wf.Run(
			context.Background(),
			purpose,
			q,
			orchid.WithConstraints(orchid.Constraints{MaxSteps: 15}),
		)
		elapsed := time.Since(start)
		wf.Close()

		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		}

		fmt.Printf("═══ Q%d [%s] ═══\n", i+1, mode)
		fmt.Printf("Question: %s\n", q)
		fmt.Printf("Time: %s\n", elapsed.Round(time.Second))
		fmt.Printf("Tokens: %d in / %d out\n", usage.InputTokens, usage.OutputTokens)
		fmt.Printf("Answer:\n%s\n\n", text)
	}
}
