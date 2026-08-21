package watch

import (
	"path/filepath"
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
