package browse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSourcePrefersContentOverEmptyDrafts(t *testing.T) {
	root := t.TempDir()
	drafts := filepath.Join(root, ".wikify", "drafts")
	if err := os.MkdirAll(drafts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drafts, "wiki.json"), []byte(
		`{"pages":[{"title":"A","slug":"a","content_path":"A.md"}]}`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	content := filepath.Join(root, ".wikify", "content")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "A.md"), []byte("# A\n\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(root, ".wikify", "meta")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, "wiki.json"), []byte(
		`{"pages":[{"title":"A","slug":"a","section":"S","content_path":"A.md"}]}`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := resolveSource(root)
	if err != nil {
		t.Fatal(err)
	}
	if !src.exported {
		t.Fatalf("want exported content when drafts have no bodies, got label=%s body=%s", src.label, src.bodyDir)
	}
}

func TestResolveSourceKeepsDraftsWhenBodiesExist(t *testing.T) {
	root := t.TempDir()
	drafts := filepath.Join(root, ".wikify", "drafts")
	if err := os.MkdirAll(drafts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drafts, "wiki.json"), []byte(
		`{"pages":[{"title":"A","slug":"a"}]}`,
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drafts, "a.md"), []byte("# draft A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// published also exists
	content := filepath.Join(root, ".wikify", "content")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "A.md"), []byte("# published\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := resolveSource(root)
	if err != nil {
		t.Fatal(err)
	}
	if src.exported || !src.isDraft {
		t.Fatalf("want drafts when bodies exist, got exported=%v label=%s", src.exported, src.label)
	}
}

func TestReadPageBodyTriesContentPathTitleSlug(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "项目概述.md"), []byte("# 概述\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "Order模块"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Order模块", "Order管理.md"), []byte("# Order\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := &docSource{bodyDir: dir, exported: true}
	b, err := readPageBody(src, "4-order", "Order模块/Order管理.md")
	if err != nil || !strings.Contains(string(b), "Order") {
		t.Fatalf("content_path: err=%v body=%q", err, b)
	}
	b, err = readPageBody(src, "1-1", "项目概述.md")
	if err != nil || !strings.Contains(string(b), "概述") {
		t.Fatalf("title path: err=%v body=%q", err, b)
	}
	// non-exported drafts style
	src2 := &docSource{bodyDir: dir, exported: false}
	if err := os.WriteFile(filepath.Join(dir, "1-1.md"), []byte("# draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err = readPageBody(src2, "1-1", "项目概述.md")
	if err != nil {
		t.Fatal(err)
	}
	// drafts prefer slug.md first when present
	if !strings.Contains(string(b), "draft") && !strings.Contains(string(b), "概述") {
		t.Fatalf("draft body unexpected: %q", b)
	}
}

func TestRewriteWikiInternalLinks(t *testing.T) {
	wiki := &wikiData{Pages: []pageInfo{
		{Title: "项目概述", Slug: "1-1", ContentPath: "项目概述/项目概述.md"},
		{Title: "快速开始", Slug: "2-2", ContentPath: "快速开始.md"},
		{Title: "Order管理", Slug: "4-order", ContentPath: "Order模块/Order管理.md"},
	}}
	in := []byte(`see [快速开始](快速开始.md) and [Order](Order模块/Order管理.md) and [num](2-2) and [file](file://a.go#L1)`)
	out := string(rewriteWikiInternalLinks(in, wiki, false))
	if !strings.Contains(out, "](/2-2)") {
		t.Fatalf("want /2-2 link, got %s", out)
	}
	if !strings.Contains(out, "](/4-order)") {
		t.Fatalf("want /4-order, got %s", out)
	}
	if !strings.Contains(out, "file://a.go") {
		t.Fatalf("must keep file://, got %s", out)
	}
	out2 := string(rewriteWikiInternalLinks([]byte(`[x](快速开始.md)`), wiki, true))
	if !strings.Contains(out2, "](2-2.html)") {
		t.Fatalf("static want 2-2.html, got %s", out2)
	}
}
