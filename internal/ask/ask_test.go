package ask

import "testing"

func TestHasCitation(t *testing.T) {
	cases := []struct{ in string; want bool }{
		{`no cite`, false},
		{`[Title](slug)`, true},
		{`file://src/foo.go`, true},
		{`[a](b) and text`, true},
	}
	for _, c := range cases {
		if got := hasCitation(c.in); got != c.want {
			t.Errorf("hasCitation(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestSplitKeywords(t *testing.T) {
	kws := splitKeywords("支付流程怎么走")
	if len(kws) == 0 {
		t.Fatal("expected keywords")
	}
	found := false
	for _, k := range kws {
		if k == "支付流程怎么走" { found = true }
	}
	if !found { t.Error("whole query should be a keyword") }
}

func TestRankPages(t *testing.T) {
	pages := []rankedPage{{Title: "支付流程", Slug: "pay", Path: "business/pay.md"}, {Title: "认证", Slug: "auth", Path: "core/auth.md"}}
	contents := map[string]string{"pay": "支付包括下单", "auth": "JWT"}
	ranked := rankPages(pages, contents, "支付")
	if len(ranked) == 0 || ranked[0].Slug != "pay" {
		t.Fatalf("expected pay first, got %v", ranked)
	}
}
