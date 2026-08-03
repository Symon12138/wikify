package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Symon12138/wikify/internal/evidence"
	"github.com/Symon12138/wikify/internal/export"
	"github.com/Symon12138/wikify/internal/models"
	"github.com/Symon12138/wikify/internal/planner"
	"github.com/Symon12138/wikify/internal/scan"
	"github.com/Symon12138/wikify/internal/wikiplan"
)

func main() {
	if len(os.Args) < 2 {
		panic("usage: e2e_offline <fixture-dir>")
	}
	dir := os.Args[1]
	model, err := scan.Scan(dir, "zh", scan.Options{})
	if err != nil {
		panic(err)
	}
	plan, err := wikiplan.Read(dir)
	if err != nil {
		panic(err)
	}
	if plan != nil && plan.HasScope() {
		before := len(model.Files)
		model.ApplyScope(plan.ScopeInclude(), plan.ScopeExclude())
		fmt.Printf("scope: files %d -> %d\n", before, len(model.Files))
		for _, f := range model.Files {
			if strings.Contains(filepath.ToSlash(f.RelativePath), "/test/") ||
				strings.HasPrefix(filepath.ToSlash(f.RelativePath), "src/test/") {
				panic("test file leaked: " + f.RelativePath)
			}
		}
	}

	wiki := planner.Build(model, plan, planner.Options{MaxPages: 30})
	planner.ApplyHierarchyPaths(wiki)
	planner.EnsureTracks(wiki)
	for i := range wiki.Pages {
		p := &wiki.Pages[i]
		if len(p.DependentFiles) == 0 {
			p.DependentFiles = evidence.PickDependentFiles(model, p.Title, p.Goal, 6)
		}
		if p.DescriptionSlug == "" {
			p.DescriptionSlug = planner.DescriptionSlug(p.Title)
		}
	}

	contents := map[string]string{}
	for i, p := range wiki.Pages {
		if i%4 == 0 {
			contents[p.Slug] = ""
			continue
		}
		src := "src/main/java/com/demo/order/OrderService.java"
		if len(p.DependentFiles) > 0 {
			src = p.DependentFiles[0]
		}
		contents[p.Slug] = fmt.Sprintf(
			"## 目的\n\n说明 %s 的职责与边界，覆盖主流程与关键约束。\n\n## 结构\n\n基于源码梳理模块关系与依赖。\n\nSources: file://%s\n",
			p.Title, src,
		)
	}

	if err := export.Export(dir, model, wiki, contents, export.ExportOptions{Lang: "zh"}); err != nil {
		panic(err)
	}
	if err := export.Polish(dir, export.ExportOptions{Lang: "zh"}); err != nil {
		panic(err)
	}

	root := filepath.Join(dir, ".wikify")
	for _, c := range []string{
		filepath.Join(root, "content"),
		filepath.Join(root, "meta", "wiki-metadata.json"),
		filepath.Join(root, "meta", "browse-index.json"),
		filepath.Join(root, "meta", "wiki.json"),
		filepath.Join(root, "lang"),
	} {
		if _, err := os.Stat(c); err != nil {
			panic("missing " + c)
		}
	}

	wikiB, err := os.ReadFile(filepath.Join(root, "meta", "wiki.json"))
	if err != nil {
		panic(err)
	}
	var w models.Wiki
	if err := json.Unmarshal(wikiB, &w); err != nil {
		panic(err)
	}
	metaB, _ := os.ReadFile(filepath.Join(root, "meta", "wiki-metadata.json"))
	var meta map[string]any
	_ = json.Unmarshal(metaB, &meta)

	var found, biz, tech, stubs, withDep int
	for _, p := range w.Pages {
		switch p.Track {
		case models.TrackFoundation:
			found++
		case models.TrackBusiness:
			biz++
		default:
			tech++
		}
		if len(p.DependentFiles) > 0 {
			withDep++
		}
	}
	_ = filepath.Walk(filepath.Join(root, "content"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		b, _ := os.ReadFile(path)
		if export.IsStubPage(string(b)) {
			stubs++
		}
		return nil
	})

	fmt.Printf("pages=%d foundation=%d business=%d technical=%d withDep=%d stubs=%d\n",
		len(w.Pages), found, biz, tech, withDep, stubs)
	if len(w.Pages) == 0 || found == 0 || tech == 0 {
		panic(fmt.Sprintf("bad rails pages=%d f=%d b=%d t=%d", len(w.Pages), found, biz, tech))
	}
	if withDep == 0 {
		panic("no dependent_files")
	}
	fmt.Print("meta keys:")
	for k := range meta {
		fmt.Print(" ", k)
	}
	fmt.Println()

	browseB, _ := os.ReadFile(filepath.Join(root, "meta", "browse-index.json"))
	var browse map[string]any
	_ = json.Unmarshal(browseB, &browse)
	pages, _ := browse["pages"].([]any)
	fmt.Printf("browse pages=%d\n", len(pages))
	if len(pages) == 0 {
		panic("empty browse")
	}
	fmt.Println("OFFLINE_E2E_OK")
}
