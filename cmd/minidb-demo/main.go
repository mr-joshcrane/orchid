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
const maxFixAttempts = 3

type project struct {
	name string
	spec string
	plan string
}

var projects = []project{
	{
		name: "minidb",
		plan: `type DB struct {
	tables map[string]*Table
}

type Table struct {
	rows    map[string]map[string]string
	indexes map[string]map[string][]string
}

func NewDB() *DB
func (db *DB) CreateTable(name string) error
func (db *DB) Table(name string) (*Table, error)
func (t *Table) Insert(id string, row map[string]string) error
func (t *Table) Get(id string) (map[string]string, bool)
func (t *Table) Delete(id string) error
func (t *Table) Query(field string, value string) []map[string]string
func (t *Table) AddIndex(field string)`,
	},
	{
		name: "minicache",
		plan: `type Cache struct {
	entries map[string]entry
}

type entry struct {
	value     string
	expiresAt time.Time
}

func New() *Cache
func (c *Cache) Set(key string, value string, ttl time.Duration)
func (c *Cache) Get(key string) (string, bool)
func (c *Cache) Delete(key string)
func (c *Cache) Cleanup() int
func (c *Cache) Len() int
func (c *Cache) Keys() []string`,
	},
	{
		name: "minibus",
		plan: `type Bus struct {
	subs   map[string]map[int]func(string)
	nextID int
}

func New() *Bus
func (b *Bus) Subscribe(topic string, fn func(string)) int
func (b *Bus) Unsubscribe(id int) bool
func (b *Bus) Publish(topic string, payload string)
func (b *Bus) Topics() []string
func (b *Bus) Close()`,
	},
	{
		name: "minirouter",
		plan: `type Router struct {
	routes []route
}

type route struct {
	method  string
	parts   []string
	handler func(http.ResponseWriter, *http.Request, Params)
}

type Params map[string]string

func New() *Router
func (r *Router) Handle(method string, pattern string, handler func(http.ResponseWriter, *http.Request, Params))
func (r *Router) Lookup(method string, path string) (func(http.ResponseWriter, *http.Request, Params), Params, bool)
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request)`,
	},
	{
		name: "minifsm",
		plan: `type FSM struct {
	initial     string
	current     string
	transitions map[string]map[string]transition
}

type transition struct {
	to     string
	action func() error
}

func New(initial string) *FSM
func (f *FSM) On(from string, event string, to string, action func() error)
func (f *FSM) Send(event string) error
func (f *FSM) State() string
func (f *FSM) Can(event string) bool
func (f *FSM) Reset()`,
	},
	{
		name: "minilog",
		plan: `type Level int

const (
	LevelInfo Level = iota
	LevelWarn
	LevelError
)

type Logger struct {
	w         io.Writer
	minLevel  Level
	fields    map[string]string
	formatter func(Level, string, map[string]string) string
}

func New(w io.Writer) *Logger
func (l *Logger) WithFields(fields map[string]string) *Logger
func (l *Logger) SetLevel(level Level)
func (l *Logger) SetFormatter(fn func(Level, string, map[string]string) string)
func (l *Logger) log(level Level, msg string, kvs []string)
func (l *Logger) Info(msg string, kvs ...string)
func (l *Logger) Warn(msg string, kvs ...string)
func (l *Logger) Error(msg string, kvs ...string)`,
	},
}

func main() {
	selected := projects
	if len(os.Args) > 1 {
		name := os.Args[1]
		for _, p := range projects {
			if p.name == name {
				selected = []project{p}
				break
			}
		}
	}

	for _, p := range selected {
		fmt.Printf("\n========== Building %s ==========\n\n", p.name)
		buildProject(p)
		fmt.Printf("\n========== Done: %s ==========\n\n", p.name)
	}
}

func buildProject(p project) {
	base, _ := os.Getwd()
	outDir := p.name
	os.RemoveAll(filepath.Join(base, outDir))

	fmt.Println("=== Phase 1: Plan ===")
	typePlan, signatures := plan(p)
	fmt.Println("Signatures:", signatures)

	if len(signatures) == 0 {
		fmt.Println("No signatures found in plan. Skipping.")
		return
	}

	fmt.Println("\n=== Phase 2: Implement ===")
	implementations := implement(p, typePlan, signatures)

	fmt.Println("\n=== Phase 3: Integrate ===")
	code := integrate(p, typePlan, signatures, implementations)

	fmt.Println("\n=== Phase 4: Compile ===")
	code = compileLoop(code, outDir)

	fmt.Println("\n=== Phase 5: Vet ===")
	vetOutput := vetCheck(outDir)
	if vetOutput == "" {
		fmt.Println("go vet: clean")
	} else {
		fmt.Printf("go vet:\n%s\n", vetOutput)
	}
}

func plan(p project) (string, []string) {
	var result string
	if p.plan != "" {
		result = p.plan
		fmt.Println("Plan (hardcoded):\n" + result)
	} else {
		a := orchid.New(endpoint, model)
		a.System("You are a Go architect. When asked to design a package, respond with ONLY:\n1. The type definitions (structs, named types) needed\n2. The full function/method signatures\nUse valid Go syntax. No implementations, no comments, no explanation. Example:\ntype DB struct {\n\tdata map[string]string\n}\nfunc NewDB() *DB\nfunc (db *DB) Get(key string) (string, bool)\nfunc (db *DB) Set(key string, value string)")

		result, _, _ = a.Run(p.spec)
		fmt.Println("Plan (LLM):\n" + result)
	}

	var signatures []string
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "func ") {
			signatures = append(signatures, line)
		}
	}
	return result, signatures
}

func implement(p project, typePlan string, signatures []string) []string {
	a := orchid.New(endpoint, model)
	a.System("You are a Go developer. Respond with ONLY the Go function or method body. No package, no imports, no type definitions, no markdown, no explanation. Use the exact types and signatures provided.")

	tasks := make([]string, len(signatures))
	for i, sig := range signatures {
		tasks[i] = fmt.Sprintf(
			"Here are the types and signatures for the %s package:\n\n%s\n\n"+
				"Implement ONLY this one function:\n%s",
			p.name, typePlan, sig)
	}

	results, _ := a.Fork(tasks).Join()

	for i, r := range results {
		fmt.Printf("\n--- %s ---\n%s\n", signatures[i], r)
	}

	return results
}

func integrate(p project, typePlan string, signatures []string, implementations []string) string {
	var parts strings.Builder
	for i, r := range implementations {
		fmt.Fprintf(&parts, "// %s\n%s\n\n", signatures[i], r)
	}

	a := orchid.New(endpoint, model)
	a.System("You are a Go developer. You take function implementations and assemble them into a complete, valid, compilable Go package. You decide what files to create, what types are needed, and what imports go where. Output the complete package as a single block with file boundaries marked as '// FILE: filename.go' on their own line. No markdown fences, no explanation.")

	result, _, _ := a.Run(fmt.Sprintf(
		"Assemble these function implementations into a complete Go package called %s. "+
			"Add whatever types, structs, constructors, and imports are needed to make it compile. "+
			"Mark file boundaries with '// FILE: filename.go' lines.\n\n%s",
		p.name, parts.String()))

	fmt.Println(result)
	return result
}

func compileLoop(code string, outDir string) string {
	originalCode := code
	writePackage(code, outDir)

	for attempt := 0; attempt < maxFixAttempts; attempt++ {
		errors := compileCheck(outDir)
		if errors == "" {
			fmt.Println("Compiles clean!")
			return code
		}
		fmt.Printf("\nCompile errors (attempt %d/%d):\n%s\n", attempt+1, maxFixAttempts, errors)

		if attempt == maxFixAttempts-1 {
			fmt.Println("\nGave up after", maxFixAttempts, "compile attempts. Reverting.")
			code = originalCode
			writePackage(code, outDir)
			return code
		}

		fmt.Println("\n--- Sending back for compile fixes ---")
		code = fixCode(code, errors,
			"You are a Go developer fixing compile errors. Output the complete fixed package. Use '// FILE: filename.go' to mark file boundaries. No markdown fences, no explanation.",
			"This Go package has compile errors. Fix ALL of them.")
		writePackage(code, outDir)
	}
	return code
}

func fixCode(code string, feedback string, systemPrompt string, taskPrefix string) string {
	fixer := orchid.New(endpoint, model)
	fixer.System(systemPrompt)

	fixed, _, _ := fixer.Run(fmt.Sprintf(
		"%s\n\nISSUES:\n%s\n\nCURRENT CODE:\n%s",
		taskPrefix, feedback, code))

	fmt.Println(fixed)
	return fixed
}

func vetCheck(outDir string) string {
	base, _ := os.Getwd()
	cmd := exec.Command("go", "vet", "./"+outDir+"/")
	cmd.Dir = base
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func writePackage(code string, outDir string) {
	base, _ := os.Getwd()
	dir := filepath.Join(base, outDir)
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)

	files := splitFiles(code)
	if len(files) == 0 {
		path := filepath.Join(dir, outDir+".go")
		os.WriteFile(path, []byte(code), 0o644)
		fmt.Printf("Wrote %s\n", path)
		return
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(content), 0o644)
		fmt.Printf("Wrote %s\n", path)
	}
}

func splitFiles(code string) map[string]string {
	files := make(map[string]string)
	lines := strings.Split(code, "\n")

	var currentFile string
	var buf strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, "// FILE: ") {
			if currentFile != "" {
				files[currentFile] = strings.TrimSpace(buf.String())
				buf.Reset()
			}
			currentFile = strings.TrimPrefix(line, "// FILE: ")
			currentFile = strings.TrimSpace(currentFile)
			currentFile = filepath.Base(currentFile)
			continue
		}
		if currentFile != "" {
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}

	if currentFile != "" {
		files[currentFile] = strings.TrimSpace(buf.String())
	}

	return files
}

func compileCheck(outDir string) string {
	base, _ := os.Getwd()
	cmd := exec.Command("go", "build", "./"+outDir+"/")
	cmd.Dir = base
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}
