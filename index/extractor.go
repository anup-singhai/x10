package index

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// langConfig maps file extensions to their tree-sitter language + extraction queries.
type langConfig struct {
	lang    *sitter.Language
	queries []symbolQuery
}

// symbolQuery captures a kind of symbol from the AST.
type symbolQuery struct {
	kind   string // "function" | "method" | "class" | "struct" | "interface" | etc.
	sexp   string // tree-sitter S-expression query
	nameCapture   string
	parentCapture string // optional
}

var languages = map[string]langConfig{
	".go": {
		lang: golang.GetLanguage(),
		queries: []symbolQuery{
			{
				kind:        "function",
				sexp:        `(function_declaration name: (identifier) @name) @symbol`,
				nameCapture: "name",
			},
			{
				kind:          "method",
				sexp:          `(method_declaration receiver: (parameter_list (parameter_declaration type: [(type_identifier) @parent (pointer_type (type_identifier) @parent)])) name: (field_identifier) @name) @symbol`,
				nameCapture:   "name",
				parentCapture: "parent",
			},
			{
				kind:        "struct",
				sexp:        `(type_declaration (type_spec name: (type_identifier) @name type: (struct_type))) @symbol`,
				nameCapture: "name",
			},
			{
				kind:        "interface",
				sexp:        `(type_declaration (type_spec name: (type_identifier) @name type: (interface_type))) @symbol`,
				nameCapture: "name",
			},
		},
	},
	".ts": {
		lang: typescript.GetLanguage(),
		queries: tsQueries(),
	},
	".tsx": {
		lang: tsx.GetLanguage(),
		queries: tsQueries(),
	},
	".js": {
		lang: javascript.GetLanguage(),
		queries: jsQueries(),
	},
	".jsx": {
		lang: javascript.GetLanguage(),
		queries: jsQueries(),
	},
	".py": {
		lang: python.GetLanguage(),
		queries: []symbolQuery{
			{
				kind:        "function",
				sexp:        `(function_definition name: (identifier) @name) @symbol`,
				nameCapture: "name",
			},
			{
				kind:          "method",
				sexp:          `(class_definition name: (identifier) @parent body: (block (function_definition name: (identifier) @name) @symbol))`,
				nameCapture:   "name",
				parentCapture: "parent",
			},
			{
				kind:        "class",
				sexp:        `(class_definition name: (identifier) @name) @symbol`,
				nameCapture: "name",
			},
		},
	},
	".rs": {
		lang: rust.GetLanguage(),
		queries: []symbolQuery{
			{
				kind:        "function",
				sexp:        `(function_item name: (identifier) @name) @symbol`,
				nameCapture: "name",
			},
			{
				kind:        "struct",
				sexp:        `(struct_item name: (type_identifier) @name) @symbol`,
				nameCapture: "name",
			},
			{
				kind:        "enum",
				sexp:        `(enum_item name: (type_identifier) @name) @symbol`,
				nameCapture: "name",
			},
			{
				kind:        "trait",
				sexp:        `(trait_item name: (type_identifier) @name) @symbol`,
				nameCapture: "name",
			},
			{
				kind:          "method",
				sexp:          `(impl_item type: (type_identifier) @parent body: (declaration_list (function_item name: (identifier) @name) @symbol))`,
				nameCapture:   "name",
				parentCapture: "parent",
			},
		},
	},
}

func tsQueries() []symbolQuery {
	return []symbolQuery{
		{
			kind:        "function",
			sexp:        `(function_declaration name: (identifier) @name) @symbol`,
			nameCapture: "name",
		},
		{
			kind:        "function",
			sexp:        `(lexical_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])) @symbol`,
			nameCapture: "name",
		},
		{
			kind:          "method",
			sexp:          `(method_definition name: (property_identifier) @name) @symbol`,
			nameCapture:   "name",
			parentCapture: "",
		},
		{
			kind:        "class",
			sexp:        `(class_declaration name: (type_identifier) @name) @symbol`,
			nameCapture: "name",
		},
		{
			kind:        "interface",
			sexp:        `(interface_declaration name: (type_identifier) @name) @symbol`,
			nameCapture: "name",
		},
		{
			kind:        "type",
			sexp:        `(type_alias_declaration name: (type_identifier) @name) @symbol`,
			nameCapture: "name",
		},
	}
}

func jsQueries() []symbolQuery {
	return []symbolQuery{
		{
			kind:        "function",
			sexp:        `(function_declaration name: (identifier) @name) @symbol`,
			nameCapture: "name",
		},
		{
			kind:        "function",
			sexp:        `(lexical_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])) @symbol`,
			nameCapture: "name",
		},
		{
			kind:        "method",
			sexp:        `(method_definition name: (property_identifier) @name) @symbol`,
			nameCapture: "name",
		},
		{
			kind:        "class",
			sexp:        `(class_declaration name: (identifier) @name) @symbol`,
			nameCapture: "name",
		},
	}
}

// extractSymbols parses a source file and returns all symbols found.
func extractSymbols(filePath, relPath string, src []byte) ([]Symbol, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	cfg, ok := languages[ext]
	if !ok {
		return nil, nil // unsupported language, skip
	}

	parser := sitter.NewParser()
	parser.SetLanguage(cfg.lang)

	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}
	defer tree.Close()

	lines := strings.Split(string(src), "\n")
	var symbols []Symbol
	seen := map[string]bool{}

	for _, q := range cfg.queries {
		results, err := runQuery(cfg.lang, q, tree.RootNode(), src)
		if err != nil {
			continue
		}

		for _, r := range results {
			startLine := int(r.symbol.StartPoint().Row) + 1
			endLine := int(r.symbol.EndPoint().Row) + 1

			id := fmt.Sprintf("%s:%d:%s", relPath, startLine, r.name)
			if seen[id] {
				continue
			}
			seen[id] = true

			source := extractLines(lines, startLine-1, endLine-1)

			symbols = append(symbols, Symbol{
				ID:        id,
				Name:      r.name,
				Kind:      q.kind,
				File:      relPath,
				StartLine: startLine,
				EndLine:   endLine,
				Source:    source,
				Parent:    r.parent,
			})
		}
	}

	return symbols, nil
}

type queryResult struct {
	symbol *sitter.Node
	name   string
	parent string
}

func runQuery(lang *sitter.Language, q symbolQuery, root *sitter.Node, src []byte) ([]queryResult, error) {
	query, err := sitter.NewQuery([]byte(q.sexp), lang)
	if err != nil {
		return nil, err
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, root)

	var results []queryResult
	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		var (
			symbolNode *sitter.Node
			name, parent string
		)

		for _, capture := range match.Captures {
			captureName := query.CaptureNameForId(capture.Index)
			switch captureName {
			case "symbol":
				symbolNode = capture.Node
			case q.nameCapture:
				name = capture.Node.Content(src)
			case q.parentCapture:
				if q.parentCapture != "" {
					parent = capture.Node.Content(src)
				}
			}
		}

		if symbolNode != nil && name != "" {
			results = append(results, queryResult{
				symbol: symbolNode,
				name:   name,
				parent: parent,
			})
		}
	}

	return results, nil
}

func extractLines(lines []string, start, end int) string {
	if start >= len(lines) {
		return ""
	}
	if end >= len(lines) {
		end = len(lines) - 1
	}
	// cap at 80 lines to avoid enormous source blobs
	if end-start > 80 {
		end = start + 80
	}
	return strings.Join(lines[start:end+1], "\n")
}

// SupportedExtensions returns all file extensions the indexer handles.
func SupportedExtensions() []string {
	exts := make([]string, 0, len(languages))
	for ext := range languages {
		exts = append(exts, ext)
	}
	return exts
}
