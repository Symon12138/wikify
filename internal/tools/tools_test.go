package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestViewFileInDetailNormalRange(t *testing.T) {
	dir := t.TempDir()
	name := writeTempFile(t, dir, "sample.txt", "a\nb\nc\nd\ne\n")
	ts := New(dir)
	out := ts.viewFileInDetail(map[string]any{
		"file_path":  name,
		"start_line": float64(2),
		"end_line":   float64(4),
	})
	if strings.HasPrefix(out, "Error") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "lines 2-4") {
		t.Fatalf("header: %s", out)
	}
	if !strings.Contains(out, "b\n") || !strings.Contains(out, "d") {
		t.Fatalf("body missing expected lines: %q", out)
	}
}

func TestViewFileInDetailInvertedRangeNoPanic(t *testing.T) {
	dir := t.TempDir()
	// 50 lines — mirrors panic start=99 end=50 on a short file.
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		b.WriteString("line\n")
	}
	name := writeTempFile(t, dir, "short.txt", b.String())
	ts := New(dir)

	// Exact panic shape from production: start_line=99, end_line=50.
	out := ts.viewFileInDetail(map[string]any{
		"file_path":  name,
		"start_line": float64(99),
		"end_line":   float64(50),
	})
	if strings.HasPrefix(out, "Error") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "of 50") {
		t.Fatalf("expected total 50, got: %s", out)
	}

	// start > end, both within file
	out2 := ts.viewFileInDetail(map[string]any{
		"file_path":  name,
		"start_line": float64(40),
		"end_line":   float64(10),
	})
	if strings.HasPrefix(out2, "Error") {
		t.Fatal(out2)
	}
}

func TestViewFileInDetailPastEOF(t *testing.T) {
	dir := t.TempDir()
	name := writeTempFile(t, dir, "tiny.txt", "only\n")
	ts := New(dir)
	out := ts.viewFileInDetail(map[string]any{
		"file_path":  name,
		"start_line": float64(100),
		"end_line":   float64(200),
	})
	if strings.HasPrefix(out, "Error") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "only") {
		t.Fatalf("expected last-window content: %q", out)
	}
}

func TestViewFileInDetailEmptyFile(t *testing.T) {
	dir := t.TempDir()
	name := writeTempFile(t, dir, "empty.txt", "")
	ts := New(dir)
	out := ts.viewFileInDetail(map[string]any{"file_path": name})
	if !strings.Contains(out, "empty") && !strings.Contains(out, "0 lines") {
		t.Fatalf("unexpected: %q", out)
	}
}

func TestCommandPolicyBlocksRedirects(t *testing.T) {
	cases := []struct {
		command string
		windows bool
		blocked bool
	}{
		// The two real-world incidents: PowerShell-style null redirects under
		// cmd.exe create literal files ($null, ') in the scanned repository.
		{"Get-ChildItem -Recurse 2>$null", true, true},
		{"findstr /s \"class\" *.java 2>'", true, true},
		{"dir /s /b *.java > out.txt", true, true},
		{"type pom.xml >> dump.txt", true, true},
		{"git log --oneline 2>&1", true, true}, // cmd.exe: all '>' blocked
		// POSIX null-device forms stay allowed on unix; real writes do not.
		{"grep -r \"main\" . 2>/dev/null", false, false},
		{"find . -name '*.go' > /dev/null 2>&1", false, false},
		{"ls -la > files.txt", false, true},
		{"cat go.mod &> dump.txt", false, true},
		// Plain read-only commands pass on both platforms.
		{"dir /s /b *.java", true, false},
		{"findstr /s \"TODO\" *.go", true, false},
		{"git show --stat HEAD", false, false},
	}
	for _, c := range cases {
		got := commandPolicyError(c.command, c.windows)
		if c.blocked && got == "" {
			t.Errorf("command %q (windows=%v): expected block, got pass", c.command, c.windows)
		}
		if !c.blocked && got != "" {
			t.Errorf("command %q (windows=%v): expected pass, got %q", c.command, c.windows, got)
		}
	}
}

func TestCommandPolicyBlocksPowerShellCmdlets(t *testing.T) {
	for _, cmd := range []string{
		"Get-ChildItem -Recurse -Filter *.java",
		"Select-String -Pattern \"Controller\" -Path src",
		"Get-Content pom.xml | Measure-Object -Line",
	} {
		if got := commandPolicyError(cmd, true); got == "" {
			t.Errorf("windows: expected PowerShell cmdlet %q to be blocked", cmd)
		}
		if got := commandPolicyError(cmd, false); got != "" {
			t.Errorf("unix: cmdlet check should not apply, %q got %q", cmd, got)
		}
	}
}

func TestCommandPolicyKeepsWriteTokenBlocks(t *testing.T) {
	for _, cmd := range []string{"rm -rf src", "del *.java", "mkdir foo", "curl http://x"} {
		if got := commandPolicyError(cmd, true); got == "" {
			t.Errorf("expected destructive command %q to stay blocked", cmd)
		}
	}
}
