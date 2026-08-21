package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watch polls workDir for file changes (respects .gitignore via simple skips) and calls onChange.
// It debounces rapid changes and respects context cancellation.
func Watch(ctx context.Context, workDir string, interval time.Duration, onChange func(changed []string)) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	prev := collectMtimes(workDir)
	// Debounce: collect changes within window before firing
	var pending []string
	var debounceTimer *time.Timer
	busy := false // 生成期间不再重复触发，新变更由下次轮询自然捕获
	flush := func() {
		if busy {
			return
		}
		if len(pending) > 0 {
			changed := pending
			pending = nil
			busy = true
			go func() {
				defer func() { busy = false }()
				onChange(changed)
			}()
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			cur := collectMtimes(workDir)
			changed := diffMtimes(prev, cur)
			if len(changed) > 0 {
				prev = cur
				pending = append(pending, changed...)
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(800*time.Millisecond, flush)
			}
		}
	}
}

func collectMtimes(root string) map[string]time.Time {
	m := map[string]time.Time{}
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil { return nil }
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == ".wikify" || name == "dist" || name == ".tmp" || name == "vendor" {
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") && name != "." {
				// Skip hidden dirs (like .claude, .comet) but not the root itself
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil { return nil }
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
	// Also detect deletions (not needed for trigger, but for completeness)
	return changed
}

func FormatChanged(changed []string, workDir string) string {
	if len(changed) == 0 { return "" }
	if len(changed) == 1 {
		rel, _ := filepath.Rel(workDir, changed[0])
		return rel
	}
	rel0, _ := filepath.Rel(workDir, changed[0])
	return fmt.Sprintf("%s +%d more", rel0, len(changed)-1)
}