package index

// Symbol represents a named code entity (function, class, method, struct, etc.)
type Symbol struct {
	ID        string // file:line:name
	Name      string
	Kind      string // function | method | class | struct | interface | const | type
	File      string // path relative to workspace root
	StartLine int
	EndLine   int
	Source    string // full source text of the symbol
	Parent    string // for methods: enclosing class/struct name
}

// Edge represents a relationship between two symbols.
type Edge struct {
	FromID string
	ToName string
	Kind   string // calls | imports | extends | implements | uses
}

// SearchResult is returned by FTS queries.
type SearchResult struct {
	Symbol Symbol
	Score  float64
}

// ContextBlock is a pre-assembled context chunk for LLM consumption.
type ContextBlock struct {
	File    string
	Symbols []Symbol
}
