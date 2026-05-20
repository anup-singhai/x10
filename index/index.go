package index

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// Index is the codebase knowledge graph.
type Index struct {
	workspaceDir string
	db           *sql.DB
	mu           sync.RWMutex
}

// Open opens an existing index (or creates a new empty one).
func Open(workspaceDir string) (*Index, error) {
	db, err := openDB(workspaceDir)
	if err != nil {
		return nil, err
	}
	return &Index{workspaceDir: workspaceDir, db: db}, nil
}

// Close closes the database.
func (idx *Index) Close() error {
	return idx.db.Close()
}

// Build walks the workspace and indexes all supported files.
// Progress is reported via the optional callback.
func (idx *Index) Build(progress func(done, total int, file string)) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// collect files
	var files []string
	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, ".x10": true,
		"vendor": true, "dist": true, "build": true, ".next": true,
		"target": true, "__pycache__": true, ".venv": true,
	}
	supported := map[string]bool{}
	for _, ext := range SupportedExtensions() {
		supported[ext] = true
	}

	filepath.WalkDir(idx.workspaceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if supported[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})

	total := len(files)
	var done atomic.Int64

	// clear old data
	if _, err := idx.db.Exec(`DELETE FROM symbols; DELETE FROM edges`); err != nil {
		return err
	}

	// process files with worker pool
	type job struct{ path string }
	jobs := make(chan job, total)
	for _, f := range files {
		jobs <- job{f}
	}
	close(jobs)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	workers := 8

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if err := idx.indexFile(j.path); err != nil {
					// non-fatal: log and continue
					_ = err
				}
				n := int(done.Add(1))
				if progress != nil {
					rel, _ := filepath.Rel(idx.workspaceDir, j.path)
					progress(n, total, rel)
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	// store meta
	idx.db.Exec(`INSERT OR REPLACE INTO meta VALUES ('workspace', ?)`, idx.workspaceDir)
	idx.db.Exec(`INSERT OR REPLACE INTO meta VALUES ('indexed_files', ?)`, fmt.Sprintf("%d", total))

	return nil
}

// IndexFile indexes (or re-indexes) a single file. Used for incremental updates.
func (idx *Index) IndexFile(path string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.indexFile(path)
}

// RemoveFile removes all symbols for a file from the index.
func (idx *Index) RemoveFile(path string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	rel, _ := filepath.Rel(idx.workspaceDir, path)
	_, err := idx.db.Exec(`DELETE FROM symbols WHERE file = ?`, rel)
	return err
}

func (idx *Index) indexFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	rel, _ := filepath.Rel(idx.workspaceDir, path)
	symbols, err := extractSymbols(path, rel, src)
	if err != nil {
		return err
	}
	if len(symbols) == 0 {
		return nil
	}

	// delete old symbols for this file then re-insert
	tx, err := idx.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM symbols WHERE file = ?`, rel); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO symbols (id, name, kind, file, start_line, end_line, source, parent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range symbols {
		if _, err := stmt.Exec(s.ID, s.Name, s.Kind, s.File, s.StartLine, s.EndLine, s.Source, s.Parent); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Stats returns basic index statistics.
func (idx *Index) Stats() (symbols, files int, err error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	idx.db.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&symbols)
	idx.db.QueryRow(`SELECT COUNT(DISTINCT file) FROM symbols`).Scan(&files)
	return
}
