package index

import (
	"fmt"
	"strings"
)

// Search does a full-text search across symbol names and source code.
func (idx *Index) Search(query string, limit int) ([]Symbol, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	// FTS5 query — escape special chars
	ftsQuery := sanitizeFTS(query)

	rows, err := idx.db.Query(`
		SELECT s.id, s.name, s.kind, s.file, s.start_line, s.end_line, s.source, s.parent
		FROM symbols_fts f
		JOIN symbols s ON s.id = f.id
		WHERE symbols_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSymbols(rows)
}

// Lookup returns all symbols with the given name.
func (idx *Index) Lookup(name string) ([]Symbol, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	rows, err := idx.db.Query(`
		SELECT id, name, kind, file, start_line, end_line, source, parent
		FROM symbols WHERE name = ?
	`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// FileSymbols returns all symbols in a file.
func (idx *Index) FileSymbols(relPath string) ([]Symbol, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	rows, err := idx.db.Query(`
		SELECT id, name, kind, file, start_line, end_line, source, parent
		FROM symbols WHERE file = ?
		ORDER BY start_line
	`, relPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// BuildContext assembles the most relevant context for a given task.
// It returns formatted source blocks ready to inject into a prompt.
func (idx *Index) BuildContext(task string, maxSymbols int) (string, error) {
	if maxSymbols <= 0 {
		maxSymbols = 15
	}

	symbols, err := idx.Search(task, maxSymbols)
	if err != nil || len(symbols) == 0 {
		return "", err
	}

	// group by file
	byFile := map[string][]Symbol{}
	order := []string{}
	for _, s := range symbols {
		if _, exists := byFile[s.File]; !exists {
			order = append(order, s.File)
		}
		byFile[s.File] = append(byFile[s.File], s)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Relevant code for: %s\n\n", task))

	for _, file := range order {
		syms := byFile[file]
		sb.WriteString(fmt.Sprintf("### %s\n\n", file))
		for _, s := range syms {
			label := s.Kind
			if s.Parent != "" {
				label = fmt.Sprintf("%s.%s", s.Parent, s.Name)
			} else {
				label = s.Name
			}
			sb.WriteString(fmt.Sprintf("**%s** (%s, line %d)\n```\n%s\n```\n\n", label, s.Kind, s.StartLine, s.Source))
		}
	}

	return sb.String(), nil
}

func scanSymbols(rows interface{ Next() bool; Scan(...interface{}) error; Err() error }) ([]Symbol, error) {
	var out []Symbol
	for rows.Next() {
		var s Symbol
		if err := rows.Scan(&s.ID, &s.Name, &s.Kind, &s.File, &s.StartLine, &s.EndLine, &s.Source, &s.Parent); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// sanitizeFTS converts a plain query into a safe FTS5 query.
// Wraps each term in double quotes to avoid syntax errors.
func sanitizeFTS(query string) string {
	words := strings.Fields(query)
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		// strip non-alpha chars for safety
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				return r
			}
			return -1
		}, w)
		if clean != "" {
			quoted = append(quoted, `"`+clean+`"`)
		}
	}
	if len(quoted) == 0 {
		return `"*"`
	}
	return strings.Join(quoted, " OR ")
}
