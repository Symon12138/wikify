package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// skipDir reports whether a directory should not be watched.
func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", ".wikify", "dist", ".tmp", "vendor":
		return true
	}
	return strings.HasPrefix(name, ".") && name != "."
}

// addRecursive adds root and all subdirectories to the watcher.
func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if skipDir(d.Name()) && path != root {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

// debouncer coalesces rapid events into a single batch.
type debouncer struct {
	pending map[string]bool
	delay   time.Duration
	timer   *time.Timer
	onFlush func(changed []string)
	busy    bool // onChange in flight; coalesce until it returns
}

func newDebouncer(delay time.Duration, onFlush func([]string)) *debouncer {
	return &debouncer{pending: map[string]bool{}, delay: delay, onFlush: onFlush}
}

func (d *debouncer) rearm() {
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, func() { d.flush() })
}

func (d *debouncer) add(path string) {
	d.pending[path] = true
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, d.flush)
}

func (d *debouncer) flush() {
	if d.busy {
		return // keep pending; retried via rearm after onChange returns
	}
	if len(d.pending) == 0 {
		return
	}
	changed := make([]string, 0, len(d.pending))
	for p := range d.pending {
		changed = append(changed, p)
	}
	d.pending = map[string]bool{}
	d.busy = true
	go func() {
		defer func() { d.busy = false }()
		d.onFlush(changed)
		if len(d.pending) > 0 {
			d.rearm()
		}
	}()
}

// WatchFS watches workDir with fsnotify (event-driven, no polling) and calls
// onChange with debounced changed paths. Falls back gracefully on ctx cancel.
// New directories created at runtime are watched automatically.
func WatchFS(ctx context.Context, workDir string, debounce time.Duration, onChange func(changed []string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify watcher: %w", err)
	}
	defer w.Close()

	if err := addRecursive(w, workDir); err != nil {
		return fmt.Errorf("add recursive: %w", err)
	}

	if debounce <= 0 {
		debounce = 800 * time.Millisecond
	}
	db := newDebouncer(debounce, onChange)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			// Watch newly created directories
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() && !skipDir(filepath.Base(ev.Name)) {
					_ = addRecursive(w, ev.Name)
				}
			}
			if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) || ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				db.add(ev.Name)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			// Non-fatal: skip unreadable paths, keep watching
			_ = err
		}
	}
}

// Keep the old polling API available for reference/fallback.
func collectMtimes(root string) map[string]time.Time {
	m := map[string]time.Time{}
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		m[path] = info.ModTime()
		return nil
	})
	return m
}

func diffMtimes(prev, cur map[string]time.Time) []string {
	var changed []string
	for path, curTime := range cur {
		if prevTime, ok := prev[path]; !ok || !curTime.Equal(prevTime) {
			changed = append(changed, path)
		}
	}
	return changed
}

func FormatChanged(changed []string, workDir string) string {
	if len(changed) == 0 {
		return ""
	}
	if len(changed) == 1 {
		rel, _ := filepath.Rel(workDir, changed[0])
		return rel
	}
	rel0, _ := filepath.Rel(workDir, changed[0])
	return fmt.Sprintf("%s +%d more", rel0, len(changed)-1)
}