package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"x10/providers"
)

// Handler executes a tool call and returns the result string.
type Handler func(workdir string, input map[string]interface{}) (string, error)

// Registry maps tool names to handlers and their definitions.
type Registry struct {
	defs     []providers.Tool
	handlers map[string]Handler
}

func New() *Registry {
	r := &Registry{handlers: map[string]Handler{}}
	r.register(readFileDef, readFile)
	r.register(writeFileDef, writeFile)
	r.register(editFileDef, editFile)
	r.register(bashDef, bash)
	r.register(globDef, glob)
	r.register(grepDef, grep)
	r.register(listDirDef, listDir)
	return r
}

func (r *Registry) Definitions() []providers.Tool { return r.defs }

func (r *Registry) Execute(workdir, name string, input map[string]interface{}) (string, error) {
	h, ok := r.handlers[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return h(workdir, input)
}

func (r *Registry) register(def providers.Tool, h Handler) {
	r.defs = append(r.defs, def)
	r.handlers[def.Name] = h
}

// ── tool definitions ──────────────────────────────────────────────────────────

var readFileDef = providers.Tool{
	Name:        "read_file",
	Description: "Read the contents of a file at the given path.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "Absolute or relative file path"},
		},
	},
}

var writeFileDef = providers.Tool{
	Name:        "write_file",
	Description: "Write content to a file, creating it if it does not exist.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"path", "content"},
		"properties": map[string]interface{}{
			"path":    map[string]interface{}{"type": "string"},
			"content": map[string]interface{}{"type": "string"},
		},
	},
}

var editFileDef = providers.Tool{
	Name:        "edit_file",
	Description: "Replace an exact string in a file with new content. Use for targeted edits.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"path", "old_string", "new_string"},
		"properties": map[string]interface{}{
			"path":       map[string]interface{}{"type": "string"},
			"old_string": map[string]interface{}{"type": "string", "description": "Exact text to replace (must be unique in file)"},
			"new_string": map[string]interface{}{"type": "string", "description": "Replacement text"},
		},
	},
}

var bashDef = providers.Tool{
	Name:        "bash",
	Description: "Run a shell command in the workspace directory. Returns stdout + stderr.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"command"},
		"properties": map[string]interface{}{
			"command": map[string]interface{}{"type": "string"},
		},
	},
}

var globDef = providers.Tool{
	Name:        "glob",
	Description: "Find files matching a glob pattern in the workspace.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"pattern"},
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern e.g. **/*.ts"},
		},
	},
}

var grepDef = providers.Tool{
	Name:        "grep",
	Description: "Search for a pattern in files. Returns matching lines with file:line context.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"pattern"},
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{"type": "string", "description": "Search string or regex"},
			"path":    map[string]interface{}{"type": "string", "description": "File or directory to search (default: workspace root)"},
		},
	},
}

var listDirDef = providers.Tool{
	Name:        "list_dir",
	Description: "List files and directories at a path.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
	},
}

// ── tool handlers ─────────────────────────────────────────────────────────────

func readFile(workdir string, input map[string]interface{}) (string, error) {
	p := resolvePath(workdir, str(input, "path"))
	
	// Check if file exists first
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s (if this is an image file path, use image data from the task context instead)", p)
		}
		return "", err
	}
	
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writeFile(workdir string, input map[string]interface{}) (string, error) {
	p := resolvePath(workdir, str(input, "path"))
	newContent := str(input, "content")

	// Read existing content for diff
	var oldContent string
	if data, err := os.ReadFile(p); err == nil {
		oldContent = string(data)
	}

	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(newContent), 0644); err != nil {
		return "", err
	}

	rel, err := filepath.Rel(workdir, p)
	if err != nil {
		rel = p
	}
	diff := computeLineDiff(oldContent, newContent, 3)
	return "DIFF:" + rel + "\n" + diff, nil
}

func editFile(workdir string, input map[string]interface{}) (string, error) {
	p := resolvePath(workdir, str(input, "path"))
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	content := string(data)
	old := str(input, "old_string")
	new := str(input, "new_string")

	if !strings.Contains(content, old) {
		return "", fmt.Errorf("old_string not found in %s", p)
	}
	count := strings.Count(content, old)
	if count > 1 {
		return "", fmt.Errorf("old_string found %d times in %s — must be unique", count, p)
	}

	updated := strings.Replace(content, old, new, 1)
	if err := os.WriteFile(p, []byte(updated), 0644); err != nil {
		return "", err
	}

	rel, err := filepath.Rel(workdir, p)
	if err != nil {
		rel = p
	}
	diff := computeLineDiff(old, new, 2)
	return "DIFF:" + rel + "\n" + diff, nil
}

func bash(workdir string, input map[string]interface{}) (string, error) {
	cmd := exec.Command("bash", "-c", str(input, "command"))
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	result := string(out)
	if err != nil {
		return result, fmt.Errorf("exit %v: %s", err, result)
	}
	return result, nil
}

func glob(workdir string, input map[string]interface{}) (string, error) {
	pattern := str(input, "pattern")
	matches, err := filepath.Glob(filepath.Join(workdir, pattern))
	if err != nil {
		return "", err
	}
	// make paths relative to workdir for cleaner output
	rel := make([]string, 0, len(matches))
	for _, m := range matches {
		r, _ := filepath.Rel(workdir, m)
		rel = append(rel, r)
	}
	out, _ := json.Marshal(rel)
	return string(out), nil
}

func grep(workdir string, input map[string]interface{}) (string, error) {
	pattern := str(input, "pattern")
	searchPath := workdir
	if p := str(input, "path"); p != "" {
		searchPath = resolvePath(workdir, p)
	}

	cmd := exec.Command("grep", "-rn", "--include=*", pattern, searchPath)
	out, _ := cmd.CombinedOutput()
	return string(out), nil
}

func listDir(workdir string, input map[string]interface{}) (string, error) {
	p := resolvePath(workdir, str(input, "path"))
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() {
			lines = append(lines, e.Name()+"/")
		} else {
			lines = append(lines, e.Name())
		}
	}
	return strings.Join(lines, "\n"), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func str(m map[string]interface{}, k string) string {
	v, _ := m[k].(string)
	return v
}

func resolvePath(workdir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workdir, p)
}

// ── diff helpers ──────────────────────────────────────────────────────────────

const diffMaxLines = 300 // per side; larger files get summary only

type diffLine struct {
	op   byte // '+', '-', ' '
	text string
}

// computeLineDiff returns a diff string with '+'/'-'/' ' prefixed lines.
// context is the number of unchanged lines to show around each change.
func computeLineDiff(oldText, newText string, context int) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	// New file — show up to 60 lines as added
	if len(oldLines) == 0 {
		limit := len(newLines)
		if limit > 60 {
			limit = 60
		}
		var sb strings.Builder
		for _, l := range newLines[:limit] {
			sb.WriteByte('+')
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
		if len(newLines) > 60 {
			fmt.Fprintf(&sb, "+... (%d more lines)\n", len(newLines)-60)
		}
		return sb.String()
	}

	// Too large for LCS — show summary
	if len(oldLines) > diffMaxLines || len(newLines) > diffMaxLines {
		return fmt.Sprintf("~(%d lines → %d lines)\n", len(oldLines), len(newLines))
	}

	ops := lcsLineDiff(oldLines, newLines)
	return formatDiffOps(ops, context)
}

// lcsLineDiff computes a line-level diff via LCS.
func lcsLineDiff(old, new []string) []diffLine {
	m, n := len(old), len(new)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	var ops []diffLine
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && old[i-1] == new[j-1] {
			ops = append(ops, diffLine{' ', old[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, diffLine{'+', new[j-1]})
			j--
		} else {
			ops = append(ops, diffLine{'-', old[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// formatDiffOps renders ops with context-line collapsing.
func formatDiffOps(ops []diffLine, ctx int) string {
	n := len(ops)
	shown := make([]bool, n)
	hasChange := false
	for i, op := range ops {
		if op.op != ' ' {
			hasChange = true
			for j := i - ctx; j <= i+ctx; j++ {
				if j >= 0 && j < n {
					shown[j] = true
				}
			}
		}
	}
	if !hasChange {
		return ""
	}

	var sb strings.Builder
	skipped := 0
	for i, op := range ops {
		if shown[i] {
			if skipped > 0 {
				fmt.Fprintf(&sb, " ...(%d lines)\n", skipped)
				skipped = 0
			}
			sb.WriteByte(op.op)
			sb.WriteString(op.text)
			sb.WriteByte('\n')
		} else {
			skipped++
		}
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
