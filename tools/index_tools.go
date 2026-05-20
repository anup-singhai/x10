package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"x10/index"
	"x10/providers"
)

// WithIndex returns a new Registry that includes codebase search tools
// backed by the given index.
// codebase_context is excluded when preInject=true because context is already
// pre-loaded into the conversation — exposing it as a tool just invites the
// model to call it again redundantly.
func WithIndex(idx *index.Index, preInject bool) *Registry {
	r := New()
	r.register(codebaseSearchDef, makeCodebaseSearch(idx))
	r.register(symbolLookupDef, makeSymbolLookup(idx))
	if !preInject {
		r.register(codebaseContextDef, makeCodebaseContext(idx))
	}
	return r
}

var codebaseSearchDef = providers.Tool{
	Name:        "codebase_search",
	Description: "Search the codebase index for symbols (functions, classes, methods) matching a query. Returns names, files, and line numbers. Much faster than grep for finding where things are defined.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search terms — e.g. 'auth login token', 'UserService', 'handleRequest'",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Max results (default 20)",
			},
		},
	},
}

var codebaseContextDef = providers.Tool{
	Name:        "codebase_context",
	Description: "Assemble relevant source code context for a task. Returns the most relevant function/class implementations from the indexed codebase in one call — use this before making edits to understand the area you're changing.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"task"},
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "Describe what you're trying to do — e.g. 'fix auth token refresh', 'add rate limiting to API'",
			},
			"max_symbols": map[string]interface{}{
				"type":        "integer",
				"description": "Max symbols to include (default 15)",
			},
		},
	},
}

var symbolLookupDef = providers.Tool{
	Name:        "symbol_lookup",
	Description: "Look up all definitions of a symbol by exact name. Returns all files and line numbers where it's defined.",
	InputSchema: map[string]interface{}{
		"type":     "object",
		"required": []string{"name"},
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Exact symbol name to look up",
			},
		},
	},
}

func makeCodebaseSearch(idx *index.Index) Handler {
	return func(_ string, input map[string]interface{}) (string, error) {
		query := str(input, "query")
		limit := 20
		if l, ok := input["limit"].(float64); ok {
			limit = int(l)
		}

		symbols, err := idx.Search(query, limit)
		if err != nil {
			return "", err
		}
		if len(symbols) == 0 {
			return "no symbols found", nil
		}

		var lines []string
		for _, s := range symbols {
			loc := fmt.Sprintf("%s:%d", s.File, s.StartLine)
			if s.Parent != "" {
				lines = append(lines, fmt.Sprintf("%-10s %-30s %s", s.Kind, s.Parent+"."+s.Name, loc))
			} else {
				lines = append(lines, fmt.Sprintf("%-10s %-30s %s", s.Kind, s.Name, loc))
			}
		}
		return strings.Join(lines, "\n"), nil
	}
}

func makeCodebaseContext(idx *index.Index) Handler {
	return func(_ string, input map[string]interface{}) (string, error) {
		task := str(input, "task")
		maxSymbols := 15
		if m, ok := input["max_symbols"].(float64); ok {
			maxSymbols = int(m)
		}

		ctx, err := idx.BuildContext(task, maxSymbols)
		if err != nil {
			return "", err
		}
		if ctx == "" {
			return "no relevant symbols found in index — try codebase_search or read_file", nil
		}
		return ctx, nil
	}
}

func makeSymbolLookup(idx *index.Index) Handler {
	return func(_ string, input map[string]interface{}) (string, error) {
		name := str(input, "name")
		symbols, err := idx.Lookup(name)
		if err != nil {
			return "", err
		}
		if len(symbols) == 0 {
			return fmt.Sprintf("'%s' not found in index", name), nil
		}

		out, _ := json.MarshalIndent(symbols, "", "  ")
		return string(out), nil
	}
}
