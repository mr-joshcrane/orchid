package orchid

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

func effectiveRoot(hctx HandlerContext, constructorRoot string, toolName string, warned *sync.Once) string {
	if hctx.RootDir == "" {
		return constructorRoot
	}
	warned.Do(func() {
		hctx.LogAppend(Event{
			Type:      EventRootOverride,
			ProcessID: hctx.ProcessID,
			Data: map[string]string{
				"tool":             toolName,
				"constructor_root": constructorRoot,
				"workflow_root":    hctx.RootDir,
			},
		})
	})
	return hctx.RootDir
}

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

type readConfig struct {
	maxBytes      int
	binaryDetect  bool
	offsetSupport bool
}

type ReadOption func(*readConfig)

func ReadWithMaxBytes(n int) ReadOption       { return func(c *readConfig) { c.maxBytes = n } }
func ReadWithBinaryDetect(b bool) ReadOption  { return func(c *readConfig) { c.binaryDetect = b } }
func ReadWithOffsetSupport(b bool) ReadOption { return func(c *readConfig) { c.offsetSupport = b } }

func isBinary(data []byte) bool {
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	if len(sample) == 0 {
		return false
	}
	nonPrintable := 0
	for _, b := range sample {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(len(sample)) > 0.3
}

func ReadTool(root string, opts ...ReadOption) Tool {
	cfg := readConfig{
		maxBytes:      32_000,
		binaryDetect:  true,
		offsetSupport: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	absRoot, _ := filepath.Abs(root)
	var rootWarned sync.Once

	params := map[string]Param{
		"path": {Type: "string", Description: "File path relative to project root", Required: true},
	}
	if cfg.offsetSupport {
		params["offset"] = Param{Type: "integer", Description: "Byte offset to start reading from (default 0)", Required: false}
		params["length"] = Param{Type: "integer", Description: "Number of bytes to read (default max)", Required: false}
	}

	desc := fmt.Sprintf("Read a file from %s.", root)
	if cfg.maxBytes > 0 {
		desc += fmt.Sprintf(" Files larger than %d bytes require offset and length parameters.", cfg.maxBytes)
	}

	return Tool{
		Name:           "read",
		Description:    desc,
		Params:         params,
		Parallelizable: true,
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			activeRoot := effectiveRoot(hctx, absRoot, "read", &rootWarned)

			p := args["path"]
			if p == "" {
				return "", fmt.Errorf("path is required")
			}
			target, _ := filepath.Abs(filepath.Join(activeRoot, filepath.Clean(p)))
			if !strings.HasPrefix(target, activeRoot) {
				return "", fmt.Errorf("path escapes root")
			}

			info, err := os.Stat(target)
			if err != nil {
				return "", err
			}

			if info.IsDir() {
				return readDir(target)
			}

			data, err := os.ReadFile(target)
			if err != nil {
				return "", err
			}

			if cfg.binaryDetect && isBinary(data) {
				return "TOOL_REJECTED: binary file (use a binary-capable tool or request specific byte ranges)", nil
			}

			offset := 0
			if v := args["offset"]; v != "" {
				offset, err = strconv.Atoi(v)
				if err != nil || offset < 0 {
					return "", fmt.Errorf("offset must be a non-negative integer")
				}
			}
			if offset > len(data) {
				offset = len(data)
			}
			data = data[offset:]

			length := cfg.maxBytes
			explicitRange := args["offset"] != "" || args["length"] != ""
			if v := args["length"]; v != "" {
				length, err = strconv.Atoi(v)
				if err != nil || length < 0 {
					return "", fmt.Errorf("length must be a non-negative integer")
				}
			}
			if length == 0 {
				length = len(data)
			}

			if cfg.maxBytes > 0 && length > cfg.maxBytes {
				length = cfg.maxBytes
			}

			if !explicitRange && length < len(data) {
				total := len(data) + offset
				data = data[:length]
				hint := fmt.Sprintf("\n\n[FILE TRUNCATED: showing bytes 0-%d of %d total. To read more, call read with {\"path\": %q, \"offset\": %d, \"length\": %d}]",
					length, total, args["path"], length, length)
				return string(data) + hint, nil
			}

			if length < len(data) {
				data = data[:length]
			}

			return string(data), nil
		},
	}
}

func readDir(target string) (string, error) {
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

type searchConfig struct {
	maxBytes   int
	maxMatches int
}

type SearchOption func(*searchConfig)

func SearchWithMaxBytes(n int) SearchOption   { return func(c *searchConfig) { c.maxBytes = n } }
func SearchWithMaxMatches(n int) SearchOption { return func(c *searchConfig) { c.maxMatches = n } }

func SearchTool(root string, opts ...SearchOption) Tool {
	cfg := searchConfig{
		maxBytes:   16_000,
		maxMatches: 200,
	}
	for _, o := range opts {
		o(&cfg)
	}

	absRoot, _ := filepath.Abs(root)
	var rootWarned sync.Once

	desc := fmt.Sprintf("Search file contents under %s by regex pattern. Returns matching lines with file paths and line numbers.", root)
	if cfg.maxBytes > 0 {
		desc += fmt.Sprintf(" Returns up to %d bytes.", cfg.maxBytes)
	}
	desc += fmt.Sprintf(" Maximum %d matches.", cfg.maxMatches)

	return Tool{
		Name:           "search",
		Description:    desc,
		Parallelizable: true,
		Params: map[string]Param{
			"pattern": {Type: "string", Description: "Go regex pattern to search for", Required: true},
			"include": {Type: "string", Description: "File glob filter, e.g. *.go", Required: false},
		},
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			activeRoot := effectiveRoot(hctx, absRoot, "search", &rootWarned)

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
			var totalBytes int
			filepath.Walk(activeRoot, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					if info != nil && info.IsDir() && strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
						return filepath.SkipDir
					}
					return nil
				}
				rel, _ := filepath.Rel(activeRoot, path)
				if include != "" && !matchGlob(include, info.Name()) {
					return nil
				}
				f, err := os.Open(path)
				if err != nil {
					return nil
				}
				defer f.Close()
				scanner := bufio.NewScanner(f)
				lineNum := 0
				for scanner.Scan() {
					lineNum++
					line := scanner.Text()
					if !re.MatchString(line) {
						continue
					}
					entry := fmt.Sprintf("%s:%d: %s", rel, lineNum, line)
					if cfg.maxBytes > 0 && totalBytes+len(entry)+1 > cfg.maxBytes {
						return filepath.SkipAll
					}
					matches = append(matches, entry)
					totalBytes += len(entry) + 1
					if len(matches) >= cfg.maxMatches {
						return filepath.SkipAll
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

type listConfig struct {
	maxBytes   int
	maxEntries int
}

type ListOption func(*listConfig)

func ListWithMaxBytes(n int) ListOption   { return func(c *listConfig) { c.maxBytes = n } }
func ListWithMaxEntries(n int) ListOption { return func(c *listConfig) { c.maxEntries = n } }

func ListTool(root string, opts ...ListOption) Tool {
	cfg := listConfig{
		maxBytes:   8_000,
		maxEntries: 1000,
	}
	for _, o := range opts {
		o(&cfg)
	}

	absRoot, _ := filepath.Abs(root)
	var rootWarned sync.Once

	desc := fmt.Sprintf("List all files under %s recursively. Returns relative paths.", root)
	if cfg.maxBytes > 0 {
		desc += fmt.Sprintf(" Returns up to %d bytes.", cfg.maxBytes)
	}
	desc += fmt.Sprintf(" Maximum %d entries.", cfg.maxEntries)

	return Tool{
		Name:           "list",
		Description:    desc,
		Parallelizable: true,
		Params: map[string]Param{
			"include": {Type: "string", Description: "File glob filter, e.g. *.go", Required: false},
		},
		ContextualHandler: func(hctx HandlerContext, args map[string]string) (string, error) {
			activeRoot := effectiveRoot(hctx, absRoot, "list", &rootWarned)

			include := args["include"]
			var files []string
			var totalBytes int
			filepath.Walk(activeRoot, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
						return filepath.SkipDir
					}
					return nil
				}
				if include != "" && !matchGlob(include, info.Name()) {
					return nil
				}
				rel, _ := filepath.Rel(activeRoot, path)
				if cfg.maxBytes > 0 && totalBytes+len(rel)+1 > cfg.maxBytes {
					return filepath.SkipAll
				}
				files = append(files, rel)
				totalBytes += len(rel) + 1
				if len(files) >= cfg.maxEntries {
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
