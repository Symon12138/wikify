package browse

import (
	"html/template"
	"strings"
	"testing"
)

func TestHTMLTemplateParsesAndHasNumbering(t *testing.T) {
	tmpl, err := template.New("page").Parse(htmlTmpl)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	err = tmpl.Execute(&b, pageData{
		ProjectName: "demo",
		Title:       "项目概述",
		Section:     "项目概述",
		ActiveSlug:  "overview",
		Categories: []navCategory{
			{
				Label: "项目概述",
				Index: 1,
				Track: "foundation",
				Items: []pageInfo{
					{Title: "项目概述", Slug: "overview", Index: 1, Track: "foundation"},
					{Title: "快速开始", Slug: "start", Index: 2, Track: "foundation"},
				},
			},
		},
		Content:     template.HTML("<h1>项目概述</h1>"),
		SourceLabel: "wikify",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`class="nav-num">01</span>`,
		`class="nav-num">02</span>`,
		`class="cat-idx">01</span>`,
				`window.WIKIFY_STATIC="/static"`,
		`staticBase+'/mermaid.min.js'`,
		`fx-pending`,
		`class="cat-track t-foundation"`,
		`/api/page/`,
		`function navigateTo`,
		`history.pushState`,
		`var isStatic = false`,
		`wikify-content-in`,
		`page-in`,
		`page-out`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered HTML missing %q", want)
		}
	}
	if strings.Contains(out, "cdn.jsdelivr.net") {
		t.Error("rendered HTML must not load Mermaid from CDN")
	}
	if strings.Contains(out, "gsap") || strings.Contains(out, "GSAP") {
		t.Error("rendered HTML must not reference GSAP")
	}
	// live mode must intercept nav clicks (SPA), not only full reloads
	if !strings.Contains(out, "e.preventDefault()") {
		t.Error("live SPA client should preventDefault on nav clicks")
	}
	// must not wholesale kill CSS animations (breaks all motion on Windows reduce-motion)
	if strings.Contains(out, "*,*::before,*::after{animation:none!important") {
		t.Error("must not disable all animations under prefers-reduced-motion")
	}
}

func TestStaticLinksUseRelativeAssets(t *testing.T) {
	tmpl, err := template.New("page").Parse(htmlTmpl)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	err = tmpl.Execute(&b, pageData{
		ProjectName: "demo",
		Title:       "项目概述",
		ActiveSlug:  "overview",
		StaticLinks: true,
		Categories: []navCategory{
			{Label: "项目概述", Index: 1, Items: []pageInfo{{Title: "项目概述", Slug: "overview", Index: 1}}},
		},
		Content: template.HTML("<h1>x</h1>"),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `window.WIKIFY_STATIC="static"`) {
		t.Error("static build should use relative WIKIFY_STATIC")
	}
		if !strings.Contains(out, `var isStatic = true`) {
		t.Error("static build should flag isStatic for full-page nav fallback")
	}
	if strings.Contains(out, "cdn.jsdelivr.net") {
		t.Error("static build must not use CDN")
	}
	if strings.Contains(out, "gsap") || strings.Contains(out, "GSAP") {
		t.Error("static build must not reference GSAP")
	}
}
