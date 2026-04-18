package packgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joshc/orchid"
)

type Project struct {
	Name     string
	Spec     string
	Plan     string
	Endpoint string
	Model    string
}

type Config struct {
	MaxFixAttempts int
	BaseDir        string
}

func DefaultConfig() Config {
	base, _ := os.Getwd()
	return Config{
		MaxFixAttempts: 3,
		BaseDir:        base,
	}
}

func Build(p Project, cfg Config) error {
	outDir := p.Name
	os.RemoveAll(filepath.Join(cfg.BaseDir, outDir))

	typePlan, signatures := plan(p)
	if len(signatures) == 0 {
		return fmt.Errorf("no signatures found in plan for %s", p.Name)
	}

	implementations := implement(p, typePlan, signatures)
	code := integrate(p, typePlan, signatures, implementations)
	code = compileLoop(p, code, outDir, cfg)

	vetOutput := vetCheck(outDir, cfg.BaseDir)
	if vetOutput != "" {
		fmt.Printf("go vet:\n%s\n", vetOutput)
	}

	return nil
}

func plan(p Project) (string, []string) {
	var result string
	if p.Plan != "" {
		result = p.Plan
	} else {
		a := orchid.New(p.Endpoint, p.Model)
		a.System("You are a Go architect. When asked to design a package, respond with ONLY:\n1. The type definitions (structs, named types) needed\n2. The full function/method signatures\nUse valid Go syntax. No implementations, no comments, no explanation.")
		result, _, _ = a.Run(p.Spec)
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

func implement(p Project, typePlan string, signatures []string) []string {
	a := orchid.New(p.Endpoint, p.Model)
	a.System("You are a Go developer. Respond with ONLY the Go function or method body. No package, no imports, no type definitions, no markdown, no explanation. Use the exact types and signatures provided.")

	tasks := make([]string, len(signatures))
	for i, sig := range signatures {
		tasks[i] = fmt.Sprintf(
			"Here are the types and signatures for the %s package:\n\n%s\n\nImplement ONLY this one function:\n%s",
			p.Name, typePlan, sig)
	}

	results, _ := a.Fork(tasks).Join()
	return results
}

func integrate(p Project, typePlan string, signatures []string, implementations []string) string {
	var parts strings.Builder
	for i, r := range implementations {
		fmt.Fprintf(&parts, "// %s\n%s\n\n", signatures[i], r)
	}

	a := orchid.New(p.Endpoint, p.Model)
	a.System("You are a Go developer. You take function implementations and assemble them into a complete, valid, compilable Go package. You decide what files to create, what types are needed, and what imports go where. Output the complete package as a single block with file boundaries marked as '// FILE: filename.go' on their own line. No markdown fences, no explanation.")

	result, _, _ := a.Run(fmt.Sprintf(
		"Assemble these function implementations into a complete Go package called %s. "+
			"Add whatever types, structs, constructors, and imports are needed to make it compile. "+
			"Mark file boundaries with '// FILE: filename.go' lines.\n\n%s",
		p.Name, parts.String()))

	return result
}

func compileLoop(p Project, code string, outDir string, cfg Config) string {
	original := code
	WritePackage(code, outDir, cfg.BaseDir)

	for attempt := range cfg.MaxFixAttempts {
		errors := compileCheck(outDir, cfg.BaseDir)
		if errors == "" {
			return code
		}
		fmt.Printf("compile errors (attempt %d/%d):\n%s\n", attempt+1, cfg.MaxFixAttempts, errors)

		if attempt == cfg.MaxFixAttempts-1 {
			code = original
			WritePackage(code, outDir, cfg.BaseDir)
			return code
		}

		code = fixCode(p, code, errors)
		WritePackage(code, outDir, cfg.BaseDir)
	}
	return code
}

func fixCode(p Project, code string, errors string) string {
	fixer := orchid.New(p.Endpoint, p.Model)
	fixer.System("You are a Go developer fixing compile errors. Output the complete fixed package. Use '// FILE: filename.go' to mark file boundaries. No markdown fences, no explanation.")

	fixed, _, _ := fixer.Run(fmt.Sprintf(
		"This Go package has compile errors. Fix ALL of them.\n\nISSUES:\n%s\n\nCURRENT CODE:\n%s",
		errors, code))

	return fixed
}

func compileCheck(outDir string, baseDir string) string {
	cmd := exec.Command("go", "build", "./"+outDir+"/")
	cmd.Dir = baseDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func vetCheck(outDir string, baseDir string) string {
	cmd := exec.Command("go", "vet", "./"+outDir+"/")
	cmd.Dir = baseDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func WritePackage(code string, outDir string, baseDir string) {
	dir := filepath.Join(baseDir, outDir)
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)

	files := SplitFiles(code)
	if len(files) == 0 {
		path := filepath.Join(dir, outDir+".go")
		os.WriteFile(path, []byte(code), 0o644)
		return
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(content), 0o644)
	}
}

func SplitFiles(code string) map[string]string {
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
