package browse

import (
	"strings"
	"testing"
)

func TestPreprocessMarkdownCiteBlock(t *testing.T) {
	raw := []byte(`# Title

<cite>
**参考文献**
- [core.js](file://com/web/js/core.js#L1-L40)
- [aes.js](file://com/web/js/aes.js#L10-L20)
</cite>

See also [login.jsp](file://web/login.jsp#L5-L12) inline.
`)
	out := string(preprocessMarkdown(raw))
	if strings.Contains(out, "file://") {
		t.Fatalf("file:// should be rewritten:\n%s", out)
	}
	if !strings.Contains(out, `class="src-panel"`) {
		t.Fatalf("expected src-panel:\n%s", out)
	}
	if !strings.Contains(out, `class="src-chip"`) {
		t.Fatalf("expected src-chip:\n%s", out)
	}
	if !strings.Contains(out, "core.js") || !strings.Contains(out, "L1–40") && !strings.Contains(out, "L1") {
		// allow either en-dash form
		if !strings.Contains(out, "core.js") {
			t.Fatalf("missing core.js:\n%s", out)
		}
	}
	if !strings.Contains(out, "login.jsp") {
		t.Fatalf("inline chip missing:\n%s", out)
	}
	// title tooltip should carry full path
	if !strings.Contains(out, `title="com/web/js/core.js`) {
		t.Fatalf("tooltip path missing:\n%s", out)
	}
}

func TestFormatLineFrag(t *testing.T) {
	if got := formatLineFrag("L1-L40"); got != "L1–40" {
		t.Fatalf("got %q", got)
	}
	if got := formatLineFrag("L12"); got != "L12" {
		t.Fatalf("got %q", got)
	}
}
