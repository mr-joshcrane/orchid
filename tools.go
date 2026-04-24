package orchid

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func matchGlob(pattern, name string) bool {
	if pattern == "" {
		return true
	}
	if ok, _ := filepath.Match(pattern, name); ok {
		return true
	}
	stripped := strings.TrimPrefix(pattern, "**/")
	if stripped != pattern {
		if ok, _ := filepath.Match(stripped, name); ok {
			return true
		}
	}
	return false
}

func SearchTool(root string) Tool {
	return Tool{
		Name:        "search",
		Description: "Search file contents by regex pattern. Returns matching lines with file paths and line numbers.",
		Params: map[string]Param{
			"pattern": {Type: "string", Description: "Go regex pattern to search for", Required: true},
			"include": {Type: "string", Description: "File glob filter, e.g. *.go", Required: false},
		},
		Handler: func(args map[string]string) (string, error) {
			pattern := args["pattern"]
			if pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			include := args["include"]

			var matches []string
			filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					if info != nil && info.IsDir() && strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
						return filepath.SkipDir
					}
					return nil
				}
				if include != "" {
					if !matchGlob(include, info.Name()) {
						return nil
					}
				}
				f, err := os.Open(path)
				if err != nil {
					return nil
				}
				defer f.Close()

				rel, _ := filepath.Rel(root, path)
				scanner := bufio.NewScanner(f)
				lineNum := 0
				for scanner.Scan() {
					lineNum++
					line := scanner.Text()
					if re.MatchString(line) {
						matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, lineNum, line))
						if len(matches) >= 200 {
							return filepath.SkipAll
						}
					}
				}
				return nil
			})

			if len(matches) == 0 {
				return "no matches found", nil
			}
			return strings.Join(matches, "\n"), nil
		},
	}
}

func ReadTool(root string) Tool {
	absRoot, _ := filepath.Abs(root)
	return Tool{
		Name:        "read",
		Description: "Read the contents of a file. For directories, returns a listing.",
		Params: map[string]Param{
			"path": {Type: "string", Description: "File path relative to project root", Required: true},
		},
		Handler: func(args map[string]string) (string, error) {
			p := args["path"]
			if p == "" {
				return "", fmt.Errorf("path is required")
			}
			target, _ := filepath.Abs(filepath.Join(absRoot, filepath.Clean(p)))
			if !strings.HasPrefix(target, absRoot) {
				return "", fmt.Errorf("path escapes root")
			}

			info, err := os.Stat(target)
			if err != nil {
				return "", err
			}

			if info.IsDir() {
				entries, err := os.ReadDir(target)
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
			}

			data, err := os.ReadFile(target)
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	}
}

func ListTool(root string) Tool {
	return Tool{
		Name:        "list",
		Description: "List all files in the project recursively. Returns relative paths.",
		Params: map[string]Param{
			"include": {Type: "string", Description: "File glob filter, e.g. *.go", Required: false},
		},
		Handler: func(args map[string]string) (string, error) {
			include := args["include"]
			var files []string
			filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
						return filepath.SkipDir
					}
					return nil
				}
				if include != "" {
					if !matchGlob(include, info.Name()) {
						return nil
					}
				}
				rel, _ := filepath.Rel(root, path)
				files = append(files, rel)
				if len(files) >= 1000 {
					return filepath.SkipAll
				}
				return nil
			})
			if len(files) == 0 {
				return "no files found", nil
			}
			return strings.Join(files, "\n"), nil
		},
	}
}
