package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mr-joshcrane/orchid"
)

type result struct {
	repo string
	text string
	err  error
}

func main() {
	model := os.Getenv("BEDROCK_MODEL")
	if model == "" {
		fmt.Fprintln(os.Stderr, "set BEDROCK_MODEL")
		os.Exit(1)
	}

	repos := []string{
		"cultureamp/engagement-storybook-examples",
		"cultureamp/flood-agent",
		"cultureamp/local-saml-idp",
		"cultureamp/employee-imports",
		"cultureamp/datalake-cdk-constructs",
	}

	vuln := "GHSA-phwq-j96m-2c2q"
	pkg := "pkg:npm/ejs@"
	desc := "ejs template injection vulnerability - server-side template injection in settings[view options][outputFunctionName]"

	gen, err := orchid.NewBedrockGenerator(model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create generator: %v\n", err)
		os.Exit(1)
	}

	results := make([]result, len(repos))
	var wg sync.WaitGroup
	wg.Add(len(repos))

	for i, repo := range repos {
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[i] = result{repo: repo, err: fmt.Errorf("panic: %v", r)}
				}
			}()
			results[i] = investigateRepo(gen, repo, vuln, pkg, desc)
		}()
	}
	wg.Wait()

	for _, r := range results {
		fmt.Printf("\n=== %s ===\n", r.repo)
		if r.err != nil {
			fmt.Printf("ERROR: %v\n", r.err)
			continue
		}
		fmt.Println(r.text)
	}
}

func investigateRepo(gen *orchid.BedrockGenerator, repo, vuln, pkg, desc string) result {
	fmt.Printf("Cloning %s...\n", repo)
	tmp, err := os.MkdirTemp("", "investigate-*")
	if err != nil {
		return result{repo: repo, err: fmt.Errorf("temp dir: %w", err)}
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command("git", "clone", "--depth=1", fmt.Sprintf("git@github.com:%s.git", repo), tmp)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return result{repo: repo, err: fmt.Errorf("clone: %w", err)}
	}

	agent := orchid.New(gen)
	agent.Can(
		orchid.Tool{
			Name:        "read",
			Description: "Read a file. Args: path (relative to repo root)",
			Handler: func(args map[string]string) (string, error) {
				rel := args["path"]
				full := filepath.Join(tmp, filepath.Clean(rel))
				if !strings.HasPrefix(full, tmp) {
					return "", fmt.Errorf("path escapes repo root")
				}
				data, err := os.ReadFile(full)
				if err != nil {
					return "", err
				}
				content := string(data)
				if len(content) > 8000 {
					content = content[:8000] + "\n... (truncated)"
				}
				return content, nil
			},
		},
		orchid.Tool{
			Name:        "list",
			Description: "List directory contents. Args: path (relative to repo root, default '.')",
			Handler: func(args map[string]string) (string, error) {
				rel := args["path"]
				if rel == "" {
					rel = "."
				}
				full := filepath.Join(tmp, filepath.Clean(rel))
				if !strings.HasPrefix(full, tmp) {
					return "", fmt.Errorf("path escapes repo root")
				}
				entries, err := os.ReadDir(full)
				if err != nil {
					return "", err
				}
				var lines []string
				for _, e := range entries {
					name := e.Name()
					if e.IsDir() {
						name += "/"
					}
					lines = append(lines, name)
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		orchid.Tool{
			Name:        "bash",
			Description: "Run a shell command in the repo directory. Args: command",
			Handler: func(args map[string]string) (string, error) {
				cmd := exec.Command("bash", "-c", args["command"])
				cmd.Dir = tmp
				out, err := cmd.CombinedOutput()
				result := string(out)
				if len(result) > 8000 {
					result = result[:8000] + "\n... (truncated)"
				}
				if err != nil {
					return result + "\n" + err.Error(), nil
				}
				return result, nil
			},
		},
	)
	agent.System("You are a security analyst investigating whether a specific vulnerability is reachable and exploitable in a codebase. Examine the actual code. Be specific about file paths and line numbers.")

	task := fmt.Sprintf(`Investigate whether %s (%s) is reachable and exploitable in this repository.

Vulnerability: %s
Package: %s
Description: %s

Steps:
1. Search for where this package is used (grep for imports, require statements, package.json references)
2. Trace whether user-controlled input can reach the vulnerable code path
3. Check if the vulnerable version is actually in use (check lock files)
4. Determine: is this EXPLOITABLE, NOT EXPLOITABLE IN CONTEXT, or UNCERTAIN?

Respond in EXACTLY this format:
CLASSIFICATION: <T1Critical|T2High|T3Medium|T4Low>
PREDICATE: <Exploitable|Not exploitable in context|Will not fix>
JUSTIFICATION: <specific evidence from the code — file paths, line numbers, data flow>`,
		vuln, pkg, vuln, pkg, desc)

	agent.GiveTask(task)

	step := 0
	for {
		step++
		text, effects, err := agent.Step()
		if err != nil {
			return result{repo: repo, err: fmt.Errorf("step %d: %w", step, err)}
		}
		fmt.Printf("[%s] step %d: %d effects\n", repoName(repo), step, len(effects))
		if len(effects) == 0 {
			return result{repo: repo, text: text}
		}
	}
}

func repoName(repo string) string {
	parts := strings.Split(repo, "/")
	return parts[len(parts)-1]
}
