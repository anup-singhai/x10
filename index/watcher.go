package index

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watch starts a polling file watcher that incrementally updates the index
// when source files change. Runs until the stop channel is closed.
//
// Using polling (not inotify/FSEvents) keeps the binary dependency-free.
// For a production watcher, swap this with fsnotify.
func (idx *Index) Watch(stop <-chan struct{}) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	mtimes := map[string]time.Time{}

	// seed initial mtimes
	idx.walkSources(func(path string, info os.FileInfo) {
		mtimes[path] = info.ModTime()
	})

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			idx.walkSources(func(path string, info os.FileInfo) {
				prev, seen := mtimes[path]
				mt := info.ModTime()
				if !seen || mt.After(prev) {
					mtimes[path] = mt
					_ = idx.IndexFile(path)
				}
			})

			// detect deletions
			for path := range mtimes {
				if _, err := os.Stat(path); os.IsNotExist(err) {
					_ = idx.RemoveFile(path)
					delete(mtimes, path)
				}
			}
		}
	}
}

func (idx *Index) walkSources(fn func(string, os.FileInfo)) {
	supported := map[string]bool{}
	for _, ext := range SupportedExtensions() {
		supported[ext] = true
	}
	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, ".x10": true,
		"vendor": true, "dist": true, "build": true,
	}

	filepath.Walk(idx.workspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] || strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if supported[strings.ToLower(filepath.Ext(path))] {
			fn(path, info)
		}
		return nil
	})
}
