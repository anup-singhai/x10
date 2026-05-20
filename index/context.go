package index

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BuildRichContext builds a comprehensive context block for a task.
// It always includes: file tree, README (if present), and FTS-matched symbols.
// This gives the model enough to answer broad architectural questions without
// any tool calls.
func (idx *Index) BuildRichContext(task string, maxSymbols int) (string, error) {
	if maxSymbols <= 0 {
		maxSymbols = 20
	}

	var sb strings.Builder

	// 1. file tree (compact)
	tree := idx.buildFileTree()
	if tree != "" {
		sb.WriteString("## File tree\n```\n")
		sb.WriteString(tree)
		sb.WriteString("```\n\n")
	}

	// 2. README content (first 100 lines)
	if readme := idx.readReadme(); readme != "" {
		sb.WriteString("## README\n")
		sb.WriteString(readme)
		sb.WriteString("\n\n")
	}

	// 3. FTS-matched symbols with source
	symbols, err := idx.Search(task, maxSymbols)
	if err == nil && len(symbols) > 0 {
		byFile := map[string][]Symbol{}
		order := []string{}
		for _, s := range symbols {
			if _, ok := byFile[s.File]; !ok {
				order = append(order, s.File)
			}
			byFile[s.File] = append(byFile[s.File], s)
		}

		sb.WriteString("## Relevant symbols\n\n")
		for _, file := range order {
			sb.WriteString(fmt.Sprintf("### %s\n\n", file))
			for _, s := range byFile[file] {
				label := s.Name
				if s.Parent != "" {
					label = s.Parent + "." + s.Name
				}
				sb.WriteString(fmt.Sprintf("**%s** (%s, line %d)\n```\n%s\n```\n\n", label, s.Kind, s.StartLine, s.Source))
			}
		}
	}

	return sb.String(), nil
}

func (idx *Index) buildFileTree() string {
	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, ".x10": true,
		"vendor": true, "dist": true, "build": true, "target": true,
	}

	var lines []string
	filepath.WalkDir(idx.workspaceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || len(lines) > 150 {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(idx.workspaceDir, path)
		lines = append(lines, rel)
		return nil
	})

	return strings.Join(lines, "\n")
}

func (idx *Index) readReadme() string {
	for _, name := range []string{"README.md", "readme.md", "README.txt", "README"} {
		data, err := os.ReadFile(filepath.Join(idx.workspaceDir, name))
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > 80 {
			lines = lines[:80]
		}
		return strings.Join(lines, "\n")
	}
	return ""
}
