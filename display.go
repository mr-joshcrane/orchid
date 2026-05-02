package orchid

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// PrintLog writes a human-readable audit trail of events.
func PrintLog(w io.Writer, events []Event) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\tTIME\tPROCESS\tEVENT\tDETAILS\n")
	fmt.Fprintf(tw, "--\t----\t-------\t-----\t-------\n")

	var t0 time.Time
	for _, e := range events {
		if t0.IsZero() {
			t0 = e.Timestamp
		}
		elapsed := e.Timestamp.Sub(t0)

		details := formatEventData(e)
		fmt.Fprintf(tw, "%d\t+%s\t%s\t%s\t%s\n",
			e.ID, elapsed.Truncate(time.Millisecond), e.ProcessID, e.Type, details)
	}
	tw.Flush()
}

func formatEventData(e Event) string {
	switch e.Type {
	case EventUserInput:
		return truncateStr(e.Data["content"], 80)
	case EventAssistantOutput:
		pid := e.Data["prompt_id"]
		content := truncateStr(e.Data["content"], 80)
		if pid != "" {
			return fmt.Sprintf("[%s] %s", pid, content)
		}
		return content
	case EventEffectIntent:
		return fmt.Sprintf("tool=%s %s", e.Data["tool"], truncateArgs(e.Data, "tool", "effect_id"))
	case EventEffectRejected:
		return fmt.Sprintf("reason=%q", e.Data["reason"])
	case EventEffectObserved:
		return fmt.Sprintf("tool=%s result=%s", e.Data["tool"], truncateStr(e.Data["result"], 60))
	case EventProcessSpawned:
		if e.Data["parent"] != "" {
			return fmt.Sprintf("name=%s parent=%s", e.Data["name"], e.Data["parent"])
		}
		return fmt.Sprintf("name=%s", e.Data["name"])
	default:
		id, hasID := e.Data["effect_id"]
		tool := e.Data["tool"]
		if hasID && tool != "" {
			return fmt.Sprintf("effect=%s tool=%s", id, tool)
		}
		if hasID {
			return fmt.Sprintf("effect=%s", id)
		}
		return ""
	}
}

func truncateStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func truncateArgs(data map[string]string, skip ...string) string {
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[s] = true
	}
	var parts []string
	for k, v := range data {
		if skipSet[k] {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, truncateStr(v, 40)))
	}
	return strings.Join(parts, " ")
}

type ProcessNode struct {
	ID          string
	Name        string
	Model       string
	OwnUsage    TokenUsage
	ChildUsage  TokenUsage
	TotalUsage  TokenUsage
	Children    []*ProcessNode
	StepCount   int
	EffectCount int
}

func BuildProcessTree(events []Event) []*ProcessNode {
	nodes := map[string]*ProcessNode{}
	parentOf := map[string]string{}

	for _, e := range events {
		switch e.Type {
		case EventProcessSpawned:
			nodes[e.ProcessID] = &ProcessNode{
				ID:    e.ProcessID,
				Name:  e.Data["name"],
				Model: e.Data["model"],
			}
			parent := e.Data["parent"]
			if parent != "" {
				parentOf[e.ProcessID] = parent
			}

		case EventAssistantOutput:
			node, ok := nodes[e.ProcessID]
			if !ok {
				continue
			}
			node.StepCount++
			inTok, _ := strconv.Atoi(e.Data["input_tokens"])
			outTok, _ := strconv.Atoi(e.Data["output_tokens"])
			node.OwnUsage.InputTokens += inTok
			node.OwnUsage.OutputTokens += outTok

		case EventEffectObserved:
			node, ok := nodes[e.ProcessID]
			if !ok {
				continue
			}
			node.EffectCount++
		}
	}

	for childID, parentID := range parentOf {
		parent, ok := nodes[parentID]
		if !ok {
			continue
		}
		child, ok := nodes[childID]
		if !ok {
			continue
		}
		parent.Children = append(parent.Children, child)
	}

	var roots []*ProcessNode
	for id, node := range nodes {
		if _, hasParent := parentOf[id]; !hasParent {
			roots = append(roots, node)
		}
	}

	sort.Slice(roots, func(i, j int) bool {
		return roots[i].ID < roots[j].ID
	})

	for _, root := range roots {
		rollupUsage(root)
	}

	return roots
}

func rollupUsage(node *ProcessNode) {
	node.ChildUsage = TokenUsage{}
	for _, child := range node.Children {
		rollupUsage(child)
		node.ChildUsage.InputTokens += child.TotalUsage.InputTokens
		node.ChildUsage.OutputTokens += child.TotalUsage.OutputTokens
	}
	node.TotalUsage = TokenUsage{
		InputTokens:  node.OwnUsage.InputTokens + node.ChildUsage.InputTokens,
		OutputTokens: node.OwnUsage.OutputTokens + node.ChildUsage.OutputTokens,
	}
}

func PrintTree(w io.Writer, roots []*ProcessNode) {
	for _, root := range roots {
		printNode(w, root, "", true)
	}
}

func printNode(w io.Writer, node *ProcessNode, prefix string, isLast bool) {
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if prefix == "" {
		connector = ""
	}

	model := ""
	if node.Model != "" {
		model = fmt.Sprintf("  model=%s", node.Model)
	}

	detail := ""
	if node.StepCount > 0 || node.EffectCount > 0 {
		detail = fmt.Sprintf("  [%d steps, %d effects]", node.StepCount, node.EffectCount)
	}

	fmt.Fprintf(w, "%s%s%s (%s)%s  tokens=%d/%d  total=%d/%d%s\n",
		prefix, connector,
		node.Name, node.ID, model,
		node.OwnUsage.InputTokens, node.OwnUsage.OutputTokens,
		node.TotalUsage.InputTokens, node.TotalUsage.OutputTokens,
		detail)

	childPrefix := prefix
	if prefix != "" {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}

	for i, child := range node.Children {
		printNode(w, child, childPrefix, i == len(node.Children)-1)
	}
}

func File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func Folder(root string, includeFilter ...string) (string, error) {
	var contents strings.Builder
	var walkErr error
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			walkErr = err
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if len(includeFilter) > 0 {
			matched := false
			for _, f := range includeFilter {
				if strings.Contains(info.Name(), f) {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}
		data, err := File(path)
		if err != nil {
			walkErr = err
			return nil
		}
		contents.WriteString(data)
		contents.WriteByte('\n')
		return nil
	})
	return contents.String(), walkErr
}
