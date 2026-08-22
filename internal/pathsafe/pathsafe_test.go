package pathsafe

import "testing"

func TestRel(t *testing.T) {
	ok := []string{"a.md", "business/pay.md", "a/b/c.md", "./x.md", "x y/z.md"}
	for _, in := range ok {
		if _, err := Rel(in); err != nil { t.Errorf("Rel(%q) unexpected err %v", in, err) }
	}
	bad := []string{"", "../evil.md", "a/../../b", "/abs/x.md", "C:/win.ini", "a/../b/../c", "..", "a/.."}
	for _, in := range bad {
		if _, err := Rel(in); err == nil { t.Errorf("Rel(%q) should be rejected", in) }
	}
}
