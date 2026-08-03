package browse

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// writeSearchFixture lays out an exported wiki (.wikify/content + meta/wiki.json)
// and returns the resolved docSource + catalog, exactly as serve() would see them.
func writeSearchFixture(t *testing.T, pages []pageInfo, bodies map[string]string) (*docSource, *wikiData) {
	t.Helper()
	dir := t.TempDir()
	contentDir := filepath.Join(dir, ".wikify", "content")
	metaDir := filepath.Join(dir, ".wikify", "meta")
	for _, p := range pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Slug + ".md"
		}
		full := filepath.Join(contentDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(bodies[p.Slug]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(wikiData{Pages: pages})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "wiki.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	src := findExportSource(dir)
	if src == nil {
		t.Fatal("findExportSource returned nil for exported layout")
	}
	wiki, err := readWiki(src)
	if err != nil {
		t.Fatal(err)
	}
	return src, wiki
}

func doSearch(t *testing.T, idx *searchIndex, q string) []searchResult {
	t.Helper()
	rec := httptest.NewRecorder()
	idx.handleSearch(rec, httptest.NewRequest("GET", "/api/search?q="+url.QueryEscape(q), nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var res []searchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
	}
	return res
}

func TestSearchTitleRankSnippetAndEmptyQuery(t *testing.T) {
	longBody := strings.Repeat("前", 80) + "唯一标记" + strings.Repeat("后", 80)
	pages := []pageInfo{
		{Title: "订单受理", Slug: "o1", Section: "订单模块", ContentPath: "订单模块/订单受理.md"},
		{Title: "订单结算", Slug: "o2", Section: "订单模块", ContentPath: "订单模块/订单结算.md"},
		{Title: "部署指南", Slug: "dep", ContentPath: "部署指南.md"},
		{Title: "长文", Slug: "long", ContentPath: "长文.md"},
	}
	bodies := map[string]string{
		"o1":   "# 订单受理\n\n受理逻辑校验参数并登记要素。\n",
		"o2":   "# 订单结算\n\n结算按日汇总核对。\n",
		"dep":  "# 部署指南\n\n部署订单处理链路时先初始化 OrderGate 组件。\n",
		"long": "# 长文\n\n" + longBody + "\n",
	}
	src, wiki := writeSearchFixture(t, pages, bodies)
	idx := newSearchIndex(src, wiki)

	// Title hits rank before the body-only hit; page order kept inside ranks.
	res := doSearch(t, idx, "订单")
	if len(res) != 3 {
		t.Fatalf("results=%d want 3: %+v", len(res), res)
	}
	if res[0].Title != "订单受理" || res[1].Title != "订单结算" || res[2].Title != "部署指南" {
		t.Fatalf("rank order wrong: %+v", res)
	}
	for _, r := range res {
		if !utf8.ValidString(r.Snippet) {
			t.Fatalf("broken UTF-8 snippet for %s: %q", r.Slug, r.Snippet)
		}
		if r.Path != "/"+r.Slug {
			t.Fatalf("path=%q want /%s", r.Path, r.Slug)
		}
	}
	if !strings.Contains(res[2].Snippet, "订单") {
		t.Fatalf("body hit snippet misses token: %q", res[2].Snippet)
	}

	// Case-insensitive ASCII matching.
	res = doSearch(t, idx, "ordergate")
	if len(res) != 1 || res[0].Slug != "dep" {
		t.Fatalf("case-insensitive hit=%+v", res)
	}

	// Rune-safe ±60 snippet with ellipses on both ends around a mid-body hit.
	res = doSearch(t, idx, "唯一标记")
	if len(res) != 1 || res[0].Slug != "long" {
		t.Fatalf("long-body hit=%+v", res)
	}
	sn := res[0].Snippet
	if !utf8.ValidString(sn) {
		t.Fatalf("broken UTF-8: %q", sn)
	}
	if !strings.HasPrefix(sn, "…") || !strings.HasSuffix(sn, "…") {
		t.Fatalf("expected ellipses on both ends: %q", sn)
	}
	if !strings.Contains(sn, "唯一标记") {
		t.Fatalf("snippet misses token: %q", sn)
	}
	if n := len([]rune(sn)); n > 60+60+len([]rune("唯一标记"))+2 {
		t.Fatalf("snippet too long: %d runes", n)
	}

	// Empty query → empty JSON array (not null).
	rec := httptest.NewRecorder()
	idx.handleSearch(rec, httptest.NewRequest("GET", "/api/search?q=", nil))
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty query body=%q want []", rec.Body.String())
	}
}

func TestSearchCapTwenty(t *testing.T) {
	var pages []pageInfo
	bodies := map[string]string{}
	for i := 0; i < 25; i++ {
		slug := fmt.Sprintf("p%02d", i)
		pages = append(pages, pageInfo{Title: fmt.Sprintf("批次页面%02d", i), Slug: slug, ContentPath: slug + ".md"})
		bodies[slug] = fmt.Sprintf("# 批次页面%02d\n\n批次说明内容。\n", i)
	}
	src, wiki := writeSearchFixture(t, pages, bodies)
	if res := doSearch(t, newSearchIndex(src, wiki), "批次"); len(res) != 20 {
		t.Fatalf("cap violated: %d results", len(res))
	}
}

func TestHandleFilePagesMissingInvalidAndPresent(t *testing.T) {
	pages := []pageInfo{{Title: "甲", Slug: "a", ContentPath: "甲.md"}}
	src, _ := writeSearchFixture(t, pages, map[string]string{"a": "# 甲\n\n正文。\n"})
	h := handleFilePages(src)

	// Old wikis: no file-page-index.json → graceful empty object.
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/file-pages", nil))
	if strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("missing index body=%q want {}", rec.Body.String())
	}

	// Corrupt file degrades the same way.
	idxPath := filepath.Join(src.catalogDir, "file-page-index.json")
	if err := os.WriteFile(idxPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/file-pages", nil))
	if strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("invalid index body=%q want {}", rec.Body.String())
	}

	// Valid index is passed through verbatim.
	payload := `{"app/src/A.java":[{"title":"甲","slug":"a","path":"甲.md"}]}`
	if err := os.WriteFile(idxPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/file-pages", nil))
	var m map[string][]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if refs := m["app/src/A.java"]; len(refs) != 1 || refs[0]["title"] != "甲" || refs[0]["slug"] != "a" {
		t.Fatalf("passthrough mismatch: %q", rec.Body.String())
	}
}
