package watcher

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher manages file system events and debounces them.
type Watcher struct {
	root     string
	notifier *fsnotify.Watcher
	mu       sync.Mutex
}

// New initializes a new Watcher for the given root directory.
func New(root string) (*Watcher, error) {
	n, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		root:     root,
		notifier: n,
	}, nil
}

// Start begins listening for file events and sends a signal on triggerChan when a valid change occurs.
func (w *Watcher) Start(ctx context.Context, triggerChan chan<- struct{}) {
	defer w.notifier.Close()

	// 1. Initial recursive scan to add directories
	w.addRecursive(w.root)

	var timer *time.Timer
	debounceDuration := 300 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return // Exit cleanly if the application shuts down

		case event, ok := <-w.notifier.Events:
			if !ok {
				return
			}

			// 2. Handle dynamically created directories (e.g., user runs `mkdir new_pkg`)
			if event.Op.Has(fsnotify.Create) {
				if stat, err := os.Stat(event.Name); err == nil && stat.IsDir() {
					if !w.shouldIgnoreDir(event.Name) {
						slog.Info("New directory detected, adding to watch list", "dir", event.Name)
						w.addRecursive(event.Name)
					}
					continue // Just adding a dir shouldn't trigger a build yet
				}
			}

			// 3. Ignore file deletions (usually temp files by editors) and filter by extension
			if event.Op.Has(fsnotify.Remove) || event.Op.Has(fsnotify.Chmod) || !w.isRelevantFile(event.Name) {
				continue
			}

			// 4. DEBOUNCING: Prevent multiple rapid rebuilds from a single "Save All"
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounceDuration, func() {
				// Non-blocking send to trigger channel
				select {
				case triggerChan <- struct{}{}:
					slog.Info("Valid file change detected", "file", filepath.Base(event.Name))
				default:
				}
			})

		case err, ok := <-w.notifier.Errors:
			if !ok {
				return
			}
			slog.Error("Watcher error", "error", err)
		}
	}
}

// addRecursive walks the directory tree and adds valid directories to the fsnotify watcher.
func (w *Watcher) addRecursive(dir string) {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Ignore permission errors or deleted files mid-walk
		}

		if d.IsDir() {
			if w.shouldIgnoreDir(path) {
				return fs.SkipDir // Prune this entire branch from the walk!
			}

			w.mu.Lock()
			err := w.notifier.Add(path)
			w.mu.Unlock()

			if err != nil {
				slog.Warn("Failed to watch directory", "dir", path, "error", err)
			}
		}
		return nil
	})

	if err != nil {
		slog.Error("Error walking directory tree", "error", err)
	}
}

// shouldIgnoreDir determines if a directory should be completely skipped.
func (w *Watcher) shouldIgnoreDir(path string) bool {
	base := filepath.Base(path)
	// Ignore hidden directories (e.g., .git, .idea, .vscode)
	if strings.HasPrefix(base, ".") && base != "." {
		return true
	}

	// Ignore heavy/build directories
	switch base {
	case "node_modules", "vendor", "bin", "tmp", "dist", "build":
		return true
	}
	return false
}

// isRelevantFile determines if a file change should trigger a rebuild.
func (w *Watcher) isRelevantFile(path string) bool {
	base := filepath.Base(path)
	
	// Ignore hidden files or temp swap files created by editors (like Vim or VS Code)
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") {
		return false
	}

	ext := filepath.Ext(path)
	switch ext {
	case ".go", ".json", ".env", ".yaml", ".yml", ".html", ".tmpl":
		return true
	}
	
	return false
}