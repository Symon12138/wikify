package watch

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFormatChanged(t *testing.T) {
	if got := FormatChanged(nil, "/tmp"); got != "" { t.Errorf("empty %q", got) }
	if got := FormatChanged([]string{filepath.Join("/tmp", "a.go")}, "/tmp"); got != "a.go" { t.Errorf("single %q", got) }
	if got := FormatChanged([]string{"/tmp/a.go", "/tmp/b.go"}, "/tmp"); got != "a.go +1 more" { t.Errorf("multi %q", got) }
}

func TestDiffMtimes(t *testing.T) {
	now := time.Now()
	prev := map[string]time.Time{"/a": now}
	cur := map[string]time.Time{"/a": now, "/b": now}
	changed := diffMtimes(prev, cur)
	if len(changed) != 1 || changed[0] != "/b" { t.Fatalf("expected /b, got %v", changed) }
}

func TestDebouncerCoalesces(t *testing.T) {
	var mu sync.Mutex
	var got [][]string
	done := make(chan struct{})
	db := newDebouncer(50*time.Millisecond, func(changed []string) {
		mu.Lock()
		got = append(got, changed)
		mu.Unlock()
		close(done)
	})
	db.add("/a")
	db.add("/b")
	db.add("/c")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("debounce never fired")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 { t.Fatalf("expected 1 flush, got %d", len(got)) }
	if len(got[0]) != 3 { t.Fatalf("expected 3 paths, got %v", got[0]) }
}

func TestSkipDir(t *testing.T) {
	if !skipDir(".git") { t.Error(".git should be skipped") }
	if !skipDir("node_modules") { t.Error("node_modules should be skipped") }
	if !skipDir(".claude") { t.Error(".claude should be skipped") }
	if skipDir("src") { t.Error("src should not be skipped") }
}
