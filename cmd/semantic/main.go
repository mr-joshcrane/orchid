package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Chunk is a single semantic unit extracted from a Go source file.
type Chunk struct {
	File      string    `json:"file"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // func, method, type, const, var
	LineStart int       `json:"line_start"`
	LineEnd   int       `json:"line_end"`
	Source    string    `json:"source"`
	Embedding []float64 `json:"embedding,omitempty"`
}

// Index is the stored set of embedded chunks.
type Index struct {
	Endpoint string  `json:"endpoint"`
	Chunks   []Chunk `json:"chunks"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, `usage:
  semantic chunks <dir>             dump parsed chunks (no embedding)
  semantic rerank <dir> <query>     search using the reranking endpoint
  semantic index <dir>              parse + embed Go files, write index.json
  semantic search <query>           search the index by semantic similarity`)
		os.Exit(1)
	}

	endpoint := os.Getenv("LLM_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080"
	}
	embedEndpoint := os.Getenv("EMBED_ENDPOINT")
	if embedEndpoint == "" {
		embedEndpoint = "https://nixcariot.tail60ffe.ts.net/embeddings"
	}

	switch os.Args[1] {
	case "chunks":
		if len(os.Args) < 3 {
			fatal("usage: semantic chunks <dir>")
		}
		chunks, err := parseDir(os.Args[2])
		if err != nil {
			fatal("parse: %v", err)
		}
		for _, c := range chunks {
			fmt.Printf("%-8s %-40s %s:%d-%d  (%d bytes)\n",
				c.Kind, c.Name, c.File, c.LineStart, c.LineEnd, len(c.Source))
		}
		fmt.Fprintf(os.Stderr, "%d chunks\n", len(chunks))

	case "index":
		if len(os.Args) < 3 {
			fatal("usage: semantic index <dir>")
		}
		chunks, err := parseDir(os.Args[2])
		if err != nil {
			fatal("parse: %v", err)
		}
		// Filter out huge chunks (generated tests, etc.) — 8K bytes ≈ 2K tokens
		const maxChunkBytes = 8192
		filtered := chunks[:0]
		skipped := 0
		for _, c := range chunks {
			if len(c.Source) > maxChunkBytes {
				skipped++
				continue
			}
			filtered = append(filtered, c)
		}
		chunks = filtered
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "skipped %d chunks exceeding %d bytes\n", skipped, maxChunkBytes)
		}

		fmt.Fprintf(os.Stderr, "parsed %d chunks\n", len(chunks))

		// Process in batches using the HTTP embedding endpoint
		const batchSize = 32

		for batchStart := 0; batchStart < len(chunks); batchStart += batchSize {
			batchEnd := batchStart + batchSize
			if batchEnd > len(chunks) {
				batchEnd = len(chunks)
			}
			batch := chunks[batchStart:batchEnd]
			fmt.Fprintf(os.Stderr, "embedding batch %d-%d of %d...\n", batchStart, batchEnd-1, len(chunks))

			texts := make([]string, len(batch))
			for i, c := range batch {
				texts[i] = c.Source
			}

			embeddings, err := embedBatch(embedEndpoint, texts)
			if err != nil {
				fatal("embed batch: %v", err)
			}
			for i, emb := range embeddings {
				chunks[batchStart+i].Embedding = emb
			}
		}

		idx := Index{Endpoint: embedEndpoint, Chunks: chunks}
		f, err := os.Create("index.json")
		if err != nil {
			fatal("create index.json: %v", err)
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		if err := enc.Encode(idx); err != nil {
			fatal("encode: %v", err)
		}
		f.Close()
		fmt.Fprintf(os.Stderr, "wrote index.json (%d chunks)\n", len(chunks))

	case "search":
		if len(os.Args) < 3 {
			fatal("usage: semantic search <query>")
		}
		query := strings.Join(os.Args[2:], " ")
		idx, err := loadIndex("index.json")
		if err != nil {
			fatal("load index: %v", err)
		}

		qEmb, err := embed(embedEndpoint, query)
		if err != nil {
			fatal("embed query: %v", err)
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
		for i, r := range results[:n] {
			fmt.Printf("%2d. %.4f  %-8s %-40s %s:%d-%d\n",
				i+1, r.score, r.chunk.Kind, r.chunk.Name, r.chunk.File, r.chunk.LineStart, r.chunk.LineEnd)
		}

	case "rerank":
		if len(os.Args) < 4 {
			fatal("usage: semantic rerank <dir> <query>")
		}
		dir := os.Args[2]
		query := strings.Join(os.Args[3:], " ")
		chunks, err := parseDir(dir)
		if err != nil {
			fatal("parse: %v", err)
		}
		fmt.Fprintf(os.Stderr, "parsed %d chunks, reranking against query...\n", len(chunks))

		results, err := rerank(endpoint, query, chunks)
		if err != nil {
			fatal("rerank: %v", err)
		}

		n := 10
		if n > len(results) {
			n = len(results)
		}
		for i, r := range results[:n] {
			fmt.Printf("%2d. %.4f  %-8s %-40s %s:%d-%d\n",
				i+1, r.Score, r.Chunk.Kind, r.Chunk.Name, r.Chunk.File, r.Chunk.LineStart, r.Chunk.LineEnd)
		}

	default:
		fatal("unknown command: %s", os.Args[1])
	}
}

// --- AST parsing ---

func parseDir(dir string) ([]Chunk, error) {
	var chunks []Chunk
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "third_party" || base == "_output" || base == "staging" || base == "test" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fc, err := parseFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		chunks = append(chunks, fc...)
		return nil
	})
	return chunks, err
}

func parseFile(path string) ([]Chunk, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var chunks []Chunk

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			chunks = append(chunks, extractFunc(fset, src, path, d))

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					chunks = append(chunks, extractType(fset, src, path, d, s))
				case *ast.ValueSpec:
					chunks = append(chunks, extractValue(fset, src, path, d, s))
				}
			}
		}
	}
	return chunks, nil
}

func extractFunc(fset *token.FileSet, src []byte, path string, d *ast.FuncDecl) Chunk {
	kind := "func"
	name := d.Name.Name
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = "method"
		name = recvType(d.Recv.List[0].Type) + "." + name
	}
	start := fset.Position(d.Pos())
	end := fset.Position(d.End())

	// Include doc comment if present.
	sourceStart := d.Pos()
	if d.Doc != nil {
		sourceStart = d.Doc.Pos()
	}

	return Chunk{
		File:      path,
		Name:      name,
		Kind:      kind,
		LineStart: start.Line,
		LineEnd:   end.Line,
		Source:    string(src[fset.Position(sourceStart).Offset:end.Offset]),
	}
}

func extractType(fset *token.FileSet, src []byte, path string, gd *ast.GenDecl, s *ast.TypeSpec) Chunk {
	start := fset.Position(gd.Pos())
	end := fset.Position(gd.End())

	sourceStart := gd.Pos()
	if gd.Doc != nil {
		sourceStart = gd.Doc.Pos()
	}

	return Chunk{
		File:      path,
		Name:      s.Name.Name,
		Kind:      "type",
		LineStart: start.Line,
		LineEnd:   end.Line,
		Source:    string(src[fset.Position(sourceStart).Offset:end.Offset]),
	}
}

func extractValue(fset *token.FileSet, src []byte, path string, gd *ast.GenDecl, s *ast.ValueSpec) Chunk {
	kind := "var"
	if gd.Tok == token.CONST {
		kind = "const"
	}
	name := s.Names[0].Name
	start := fset.Position(gd.Pos())
	end := fset.Position(gd.End())

	sourceStart := gd.Pos()
	if gd.Doc != nil {
		sourceStart = gd.Doc.Pos()
	}

	return Chunk{
		File:      path,
		Name:      name,
		Kind:      kind,
		LineStart: start.Line,
		LineEnd:   end.Line,
		Source:    string(src[fset.Position(sourceStart).Offset:end.Offset]),
	}
}

func recvType(expr ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), expr)
	s := buf.String()
	s = strings.TrimPrefix(s, "*")
	return s
}

// --- LLM API calls ---

// truncateToTokens asks the server to tokenize text and truncates to maxTokens.
func truncateToTokens(endpoint, text string, maxTokens int) (string, error) {
	body, _ := json.Marshal(map[string]string{"content": text})
	resp, err := http.Post(endpoint+"/tokenize", "application/json", bytes.NewReader(body))
	if err != nil {
		return text, nil // fall through on error
	}
	defer resp.Body.Close()

	var result struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return text, nil
	}
	if len(result.Tokens) <= maxTokens {
		return text, nil
	}

	// Detokenize the truncated tokens.
	dBody, _ := json.Marshal(map[string]any{"tokens": result.Tokens[:maxTokens]})
	dResp, err := http.Post(endpoint+"/detokenize", "application/json", bytes.NewReader(dBody))
	if err != nil {
		return text, nil
	}
	defer dResp.Body.Close()

	var dResult struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(dResp.Body).Decode(&dResult); err != nil {
		return text, nil
	}
	return dResult.Content, nil
}

const maxEmbedTokens = 500

func embed(endpoint, text string) ([]float64, error) {
	results, err := embedBatch(endpoint, []string{text})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

const embedBatchSize = 32

func embedBatch(endpoint string, texts []string) ([][]float64, error) {
	body, _ := json.Marshal(map[string]any{"input": texts})
	req, err := http.NewRequest("POST", endpoint+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, fmt.Errorf("api: %s", result.Error.Message)
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Data))
	}
	// Sort by index to preserve order.
	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].Index < result.Data[j].Index
	})
	out := make([][]float64, len(texts))
	for i, d := range result.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

type RankedResult struct {
	Chunk Chunk
	Score float64
}

func rerankOne(endpoint, query, doc string) (float64, error) {
	body, _ := json.Marshal(map[string]any{
		"query":     query,
		"documents": []string{doc},
	})
	resp, err := http.Post(endpoint+"/reranking", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if result.Error != nil {
		return 0, fmt.Errorf("api: %s", result.Error.Message)
	}
	if len(result.Results) == 0 {
		return 0, fmt.Errorf("no results")
	}
	return result.Results[0].RelevanceScore, nil
}

const maxRerankTokens = 450

func rerank(endpoint, query string, chunks []Chunk) ([]RankedResult, error) {
	var ranked []RankedResult
	for i, c := range chunks {
		doc, _ := truncateToTokens(endpoint, c.Source, maxRerankTokens)
		score, err := rerankOne(endpoint, query, doc)
		if err != nil {
			return nil, fmt.Errorf("chunk %d (%s): %w", i, c.Name, err)
		}
		ranked = append(ranked, RankedResult{Chunk: c, Score: score})
		fmt.Fprintf(os.Stderr, "  [%d/%d] %.4f %s\n", i+1, len(chunks), score, c.Name)
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Score > ranked[j].Score
	})
	return ranked, nil
}

// --- Math ---

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
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// --- Helpers ---

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

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
