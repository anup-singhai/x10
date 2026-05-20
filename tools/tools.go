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
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(str(input, "content")), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("written %s", p), nil
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
	return fmt.Sprintf("edited %s", p), nil
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
