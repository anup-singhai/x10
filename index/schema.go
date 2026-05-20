package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS symbols (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,
    file        TEXT NOT NULL,
    start_line  INTEGER NOT NULL,
    end_line    INTEGER NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    parent      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_kind ON symbols(kind);

CREATE TABLE IF NOT EXISTS edges (
    from_id  TEXT NOT NULL,
    to_name  TEXT NOT NULL,
    kind     TEXT NOT NULL,
    PRIMARY KEY (from_id, to_name, kind)
);

CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_to   ON edges(to_name);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);

CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
    id UNINDEXED,
    name,
    kind UNINDEXED,
    file,
    source,
    content='symbols',
    content_rowid='rowid'
);

-- keep FTS in sync
CREATE TRIGGER IF NOT EXISTS symbols_ai AFTER INSERT ON symbols BEGIN
    INSERT INTO symbols_fts(rowid, id, name, kind, file, source)
    VALUES (new.rowid, new.id, new.name, new.kind, new.file, new.source);
END;

CREATE TRIGGER IF NOT EXISTS symbols_ad AFTER DELETE ON symbols BEGIN
    INSERT INTO symbols_fts(symbols_fts, rowid, id, name, kind, file, source)
    VALUES ('delete', old.rowid, old.id, old.name, old.kind, old.file, old.source);
END;

CREATE TRIGGER IF NOT EXISTS symbols_au AFTER UPDATE ON symbols BEGIN
    INSERT INTO symbols_fts(symbols_fts, rowid, id, name, kind, file, source)
    VALUES ('delete', old.rowid, old.id, old.name, old.kind, old.file, old.source);
    INSERT INTO symbols_fts(rowid, id, name, kind, file, source)
    VALUES (new.rowid, new.id, new.name, new.kind, new.file, new.source);
END;
`

// openDB opens (or creates) the index database at workspaceDir/.x10/index.db
func openDB(workspaceDir string) (*sql.DB, error) {
	dir := filepath.Join(workspaceDir, ".x10")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, "index.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return db, nil
}
