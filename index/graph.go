package index

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ContextGraph represents a minimal dependency graph for a given task.
// It contains only the symbols reachable from entry points up to a max depth.
type ContextGraph struct {
	Roots      []Symbol           // entry point symbols mentioned in task
	Deps       map[string][]string // symbol ID → list of called symbol names
	Symbols    map[string]Symbol   // symbol ID → full symbol details
	MaxDepth   int                 // max hops from roots
	TotalSize  int                 // approximate token count
	TaskHash   string              // hash of the task string (for caching)
}

// CallExtractor parses source code to find function/method calls.
// Language-specific implementations extract calls from AST.
type CallExtractor interface {
	ExtractCalls(source string) []string
}

// GoCallExtractor extracts Go function calls.
type GoCallExtractor struct{}

func (e *GoCallExtractor) ExtractCalls(source string) []string {
	// Pattern: function_name(
	// Matches both qualified calls (pkg.Func) and unqualified (Func)
	re := regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_.]*)\s*\(`)
	matches := re.FindAllStringSubmatch(source, -1)

	seen := map[string]bool{}
	var calls []string
	for _, m := range matches {
		name := m[1]
		// Skip control flow and builtins
		if !isBuiltin(name) && !seen[name] {
			calls = append(calls, name)
			seen[name] = true
		}
	}
	return calls
}

// JSCallExtractor extracts JavaScript/TypeScript function calls.
type JSCallExtractor struct{}

func (e *JSCallExtractor) ExtractCalls(source string) []string {
	// Pattern: function_name( or object.method(
	re := regexp.MustCompile(`\b([a-zA-Z_$][a-zA-Z0-9_$]*)\s*\(`)
	matches := re.FindAllStringSubmatch(source, -1)

	seen := map[string]bool{}
	var calls []string
	for _, m := range matches {
		name := m[1]
		if !isBuiltin(name) && !seen[name] {
			calls = append(calls, name)
			seen[name] = true
		}
	}
	return calls
}

// PyCallExtractor extracts Python function calls.
type PyCallExtractor struct{}

func (e *PyCallExtractor) ExtractCalls(source string) []string {
	// Pattern: function_name(
	re := regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)
	matches := re.FindAllStringSubmatch(source, -1)

	seen := map[string]bool{}
	var calls []string
	for _, m := range matches {
		name := m[1]
		if !isBuiltinPy(name) && !seen[name] {
			calls = append(calls, name)
			seen[name] = true
		}
	}
	return calls
}

func getCallExtractor(fileExt string) CallExtractor {
	switch fileExt {
	case ".go":
		return &GoCallExtractor{}
	case ".ts", ".tsx", ".js", ".jsx":
		return &JSCallExtractor{}
	case ".py":
		return &PyCallExtractor{}
	default:
		return &JSCallExtractor{} // default
	}
}

func isBuiltin(name string) bool {
	builtins := map[string]bool{
		"len": true, "cap": true, "make": true, "new": true, "append": true,
		"copy": true, "close": true, "delete": true, "complex": true,
		"real": true, "imag": true, "panic": true, "recover": true,
		"print": true, "println": true, "fmt": true, "if": true,
		"for": true, "switch": true, "case": true, "default": true,
		"return": true, "go": true, "defer": true, "select": true,
	}
	return builtins[name]
}

func isBuiltinPy(name string) bool {
	builtins := map[string]bool{
		"len": true, "print": true, "range": true, "enumerate": true,
		"zip": true, "map": true, "filter": true, "sorted": true,
		"reversed": true, "sum": true, "min": true, "max": true,
		"abs": true, "round": true, "int": true, "str": true, "list": true,
		"dict": true, "set": true, "tuple": true, "bool": true, "float": true,
		"if": true, "for": true, "while": true, "def": true, "class": true,
		"return": true, "yield": true, "break": true, "continue": true,
		"pass": true, "raise": true, "try": true, "except": true,
	}
	return builtins[name]
}

// BuildContextGraph builds a minimal dependency graph for the given task.
// It limits results to stay under maxTokens budget.
func (idx *Index) BuildContextGraph(task string, maxTokens int) *ContextGraph {
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	graph := &ContextGraph{
		Deps:     make(map[string][]string),
		Symbols:  make(map[string]Symbol),
		MaxDepth: 3,
		TaskHash: HashString(task),
	}

	// Phase 1: Find entry point symbols from FTS search
	roots, _ := idx.Search(task, 15) // start with top 15 matches
	if len(roots) == 0 {
		return graph
	}

	// Phase 2: Add roots and walk their dependency tree
	for _, root := range roots {
		graph.Roots = append(graph.Roots, root)
		graph.Symbols[root.ID] = root
		graph.TotalSize += estimateTokens(root.Source)
	}

	// Phase 3: Walk dependency graph with token budget
	visited := make(map[string]bool)
	for _, root := range roots {
		if graph.TotalSize > maxTokens {
			break
		}
		idx.walkDeps(root, graph, visited, 0, maxTokens)
	}

	return graph
}

// walkDeps recursively adds dependencies up to maxDepth, respecting token budget.
func (idx *Index) walkDeps(sym Symbol, graph *ContextGraph, visited map[string]bool, depth int, maxTokens int) {
	if depth >= graph.MaxDepth || graph.TotalSize > maxTokens {
		return
	}
	if visited[sym.ID] {
		return
	}
	visited[sym.ID] = true

	// Extract function calls from this symbol's source
	ext := strings.ToLower(getFileExt(sym.File))
	extractor := getCallExtractor(ext)
	calls := extractor.ExtractCalls(sym.Source)

	if len(calls) > 0 {
		graph.Deps[sym.ID] = calls
	}

	// For each call, try to find the definition and add it
	for _, callName := range calls {
		if graph.TotalSize > maxTokens {
			break
		}

		// Look up the called function
		callees, _ := idx.Lookup(callName)
		if len(callees) == 0 {
			continue
		}

		// Add the first match (best guess if multiple)
		callee := callees[0]
		if visited[callee.ID] {
			continue
		}

		tokenSize := estimateTokens(callee.Source)
		if graph.TotalSize+tokenSize > maxTokens {
			// Skip this one if it would exceed budget
			continue
		}

		graph.Symbols[callee.ID] = callee
		graph.TotalSize += tokenSize

		// Recursively walk this callee's dependencies
		idx.walkDeps(callee, graph, visited, depth+1, maxTokens)
	}
}

// Format renders the context graph as formatted source blocks for LLM injection.
func (g *ContextGraph) Format() string {
	if len(g.Symbols) == 0 {
		return ""
	}

	// Group by file
	byFile := make(map[string][]Symbol)
	var fileOrder []string

	for _, sym := range g.Symbols {
		if _, exists := byFile[sym.File]; !exists {
			fileOrder = append(fileOrder, sym.File)
		}
		byFile[sym.File] = append(byFile[sym.File], sym)
	}

	var sb strings.Builder
	sb.WriteString("## Relevant symbols\n\n")

	for _, file := range fileOrder {
		symbols := byFile[file]
		sb.WriteString(fmt.Sprintf("### %s\n\n", file))

		for _, s := range symbols {
			label := s.Name
			if s.Parent != "" {
				label = s.Parent + "." + s.Name
			}
			sb.WriteString(fmt.Sprintf("**%s** (%s, line %d)\n```\n%s\n```\n\n", label, s.Kind, s.StartLine, s.Source))
		}
	}

	return sb.String()
}

// Merge combines two graphs, deduplicating symbols.
func (g *ContextGraph) Merge(other *ContextGraph) *ContextGraph {
	merged := &ContextGraph{
		Roots:    make([]Symbol, len(g.Roots)),
		Deps:     make(map[string][]string),
		Symbols:  make(map[string]Symbol),
		MaxDepth: g.MaxDepth,
	}

	copy(merged.Roots, g.Roots)

	// Merge symbols
	for id, sym := range g.Symbols {
		merged.Symbols[id] = sym
		merged.TotalSize += estimateTokens(sym.Source)
	}
	for id, sym := range other.Symbols {
		if _, exists := merged.Symbols[id]; !exists {
			merged.Symbols[id] = sym
			merged.TotalSize += estimateTokens(sym.Source)
		}
	}

	// Merge dependencies
	for id, deps := range g.Deps {
		merged.Deps[id] = deps
	}
	for id, deps := range other.Deps {
		if existing, ok := merged.Deps[id]; ok {
			// Deduplicate
			depSet := make(map[string]bool)
			for _, d := range existing {
				depSet[d] = true
			}
			for _, d := range deps {
				if !depSet[d] {
					merged.Deps[id] = append(merged.Deps[id], d)
				}
			}
		} else {
			merged.Deps[id] = deps
		}
	}

	return merged
}

// Summary returns a human-readable summary of the graph.
func (g *ContextGraph) Summary() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Context Graph Summary:\n"))
	sb.WriteString(fmt.Sprintf("  Entry points: %d\n", len(g.Roots)))
	sb.WriteString(fmt.Sprintf("  Symbols: %d\n", len(g.Symbols)))
	sb.WriteString(fmt.Sprintf("  Dependencies: %d\n", len(g.Deps)))
	sb.WriteString(fmt.Sprintf("  Est. tokens: %d\n", g.TotalSize))
	return sb.String()
}

// Cache for context graphs to avoid rebuilding for similar tasks.
type GraphCache struct {
	mu     sync.RWMutex
	graphs map[string]*ContextGraph
	maxLen int
}

// NewGraphCache creates a new LRU-style cache.
func NewGraphCache(maxLen int) *GraphCache {
	return &GraphCache{
		graphs: make(map[string]*ContextGraph),
		maxLen: maxLen,
	}
}

// Get retrieves a cached graph if it exists.
func (gc *GraphCache) Get(taskHash string) (*ContextGraph, bool) {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	g, ok := gc.graphs[taskHash]
	return g, ok
}

// Set stores a graph in the cache.
func (gc *GraphCache) Set(taskHash string, graph *ContextGraph) {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	if len(gc.graphs) >= gc.maxLen {
		// Simple eviction: remove first entry
		for k := range gc.graphs {
			delete(gc.graphs, k)
			break
		}
	}

	gc.graphs[taskHash] = graph
}

// Clear empties the cache.
func (gc *GraphCache) Clear() {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	gc.graphs = make(map[string]*ContextGraph)
}

// Helper functions

func estimateTokens(source string) int {
	// Rough heuristic: ~4 characters per token
	return (len(source) + 3) / 4
}

// HashString creates a simple hash of a string for caching purposes.
func HashString(s string) string {
	h := 0
	for _, c := range s {
		h = (h << 5) - h + int(c)
	}
	return fmt.Sprintf("%x", h)
}

func getFileExt(path string) string {
	if idx := strings.LastIndexByte(path, '.'); idx != -1 {
		return path[idx:]
	}
	return ""
}
