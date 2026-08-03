package models

import (
	"strings"
	"testing"
)

func TestFormatCatalogByTrack(t *testing.T) {
	w := &Wiki{Pages: []WikiPage{
		{Title: "项目概述", Slug: "1-a", Section: "项目概述", Track: TrackFoundation},
		{Title: "客户基本信息", Slug: "2-b", Section: "客户管理模块", Track: TrackBusiness},
		{Title: "整体架构", Slug: "3-c", Section: "系统架构设计", Track: TrackTechnical},
		{Title: "接口总览", Slug: "4-d", Section: "接口文档", Track: TrackTechnical},
	}}
	out := w.FormatCatalog("2-b")
	if !strings.Contains(out, "业务能力") && !strings.Contains(out, "Business") {
		t.Fatalf("missing business rail label:\n%s", out)
	}
	if !strings.Contains(out, "技术参考") && !strings.Contains(out, "Technical") {
		t.Fatalf("missing technical rail label:\n%s", out)
	}
	if !strings.Contains(out, "入门") && !strings.Contains(out, "Foundation") {
		if !strings.Contains(out, "Foundation") && !strings.Contains(out, "入门") {
			t.Fatalf("missing foundation rail:\n%s", out)
		}
	}
	bizIdx := strings.Index(out, "客户基本信息")
	techIdx := strings.Index(out, "整体架构")
	if bizIdx < 0 || techIdx < 0 || bizIdx > techIdx {
		t.Fatalf("expected business before technical: biz=%d tech=%d\n%s", bizIdx, techIdx, out)
	}
	if !strings.Contains(out, "[You are currently here]") {
		t.Fatal("current page marker missing")
	}
}

func TestRelatedCrossTrackBusinessToTechnical(t *testing.T) {
	w := &Wiki{Pages: []WikiPage{
		{Title: "客户基本信息管理", Slug: "1", Section: "客户管理模块", Track: TrackBusiness, Goal: "客户 基本信息"},
		{Title: "客户接口索引", Slug: "2", Section: "接口文档", Track: TrackTechnical, Goal: "客户 API"},
		{Title: "部署运维", Slug: "3", Section: "部署运维", Track: TrackTechnical},
	}}
	src := w.Pages[0]
	rel := w.RelatedCrossTrack(src, 4)
	if len(rel) == 0 {
		t.Fatal("expected cross-rail related pages")
	}
	found := false
	for _, r := range rel {
		if r.Slug == "2" {
			found = true
		}
		if r.Track == TrackBusiness {
			t.Errorf("should not return same-rail business page %q", r.Title)
		}
	}
	if !found {
		t.Fatalf("expected technical 客户接口索引, got %#v", rel)
	}
}

func TestInferTrack(t *testing.T) {
	cases := []struct {
		p    WikiPage
		want string
	}{
		{WikiPage{Section: "项目概述"}, TrackFoundation},
		{WikiPage{Section: "Getting Started"}, TrackFoundation},
		{WikiPage{Section: "客户管理模块"}, TrackBusiness},
		{WikiPage{Section: "Order Management"}, TrackBusiness},
		{WikiPage{Section: "Billing Overview"}, TrackBusiness},
		{WikiPage{Section: "Cust"}, TrackBusiness}, // path-token section
		{WikiPage{Section: "接口文档"}, TrackTechnical},
		{WikiPage{Section: "系统架构设计"}, TrackTechnical},
		{WikiPage{Section: "Core Modules"}, TrackTechnical},
		{WikiPage{Title: "客户档案管理", Section: "业务"}, TrackBusiness},
		{WikiPage{Title: "Payment Processing", Section: "Payments"}, TrackBusiness},
		// Valid Track is preserved
		{WikiPage{Title: "x", Section: "接口文档", Track: TrackBusiness}, TrackBusiness},
	}
	for _, c := range cases {
		got := InferTrack(c.p)
		if got != c.want {
			t.Errorf("InferTrack(%+v)=%s want %s", c.p, got, c.want)
		}
	}
}

func TestInferTrackDoesNotTreatModuleInCoreAsBusiness(t *testing.T) {
	got := InferTrack(WikiPage{Section: "核心模块详解"})
	if got != TrackTechnical {
		t.Fatalf("core modules section should be technical, got %s", got)
	}
}

func TestInferTrackPromotesOnboardingFromBusiness(t *testing.T) {
	// LLM free catalogs often stamp 入门 pages as business — promote to foundation.
	cases := []WikiPage{
		{Title: "平台定位与适用场景", Section: "平台入门与总览", Track: TrackBusiness},
		{Title: "推荐阅读路径", Section: "平台入门与总览", Track: TrackBusiness},
		{Title: "Getting Started", Section: "Onboarding", Track: TrackBusiness},
		{Title: "快速开始与本地运行", Section: "项目概述", Track: TrackBusiness},
	}
	for _, p := range cases {
		got := InferTrack(p)
		if got != TrackFoundation {
			t.Errorf("InferTrack(%q / %q)=%s want foundation", p.Title, p.Section, got)
		}
	}
}

func TestInferTrackKeepsCapabilityAndEngRails(t *testing.T) {
	// Must not steal business clusters or eng indexes into foundation.
	cases := []struct {
		p    WikiPage
		want string
	}{
		{WikiPage{Title: "订单受理", Section: "订单模块", Track: TrackBusiness}, TrackBusiness},
		{WikiPage{Section: "Billing Overview"}, TrackBusiness},
		{WikiPage{Title: "API接口文档", Section: "接口文档", Track: TrackTechnical}, TrackTechnical},
		{WikiPage{Title: "安全与访问控制", Section: "安全", Track: TrackTechnical}, TrackTechnical},
		{WikiPage{Title: "部署与运维总览", Section: "部署与运维"}, TrackTechnical},
	}
	for _, c := range cases {
		got := InferTrack(c.p)
		if got != c.want {
			t.Errorf("InferTrack(%+v)=%s want %s", c.p, got, c.want)
		}
	}
}

func TestInferTrackCapabilityLeafNotSwallowedByOverviewSection(t *testing.T) {
	// LLM free catalogs park capability overviews under 项目概述 — keep business.
	cases := []WikiPage{
		{Title: "客户关系管理能力概览", Section: "项目概述"},
		{Title: "内控评价与风险评估能力概览", Section: "项目概述", Track: TrackFoundation},
		{Title: "内部审计与评级能力概览", Section: "项目概述", Goal: "项目概述 / 内部审计与评级能力概览"},
		{Title: "知识管理与规则管理能力概览", Section: "项目概述"},
	}
	for _, p := range cases {
		got := InferTrack(p)
		if got != TrackBusiness {
			t.Errorf("InferTrack(%q / %q)=%s want business", p.Title, p.Section, got)
		}
	}
	// Orientation leaves under the same section stay foundation.
	orient := []WikiPage{
		{Title: "项目简介与业务定位", Section: "项目概述"},
		{Title: "环境准备与依赖组件清单", Section: "快速开始"},
		{Title: "术语表与业务概念说明", Section: "项目概述"},
		{Title: "本地构建与模块编译", Section: "快速开始"},
	}
	for _, p := range orient {
		got := InferTrack(p)
		if got != TrackFoundation {
			t.Errorf("InferTrack(%q / %q)=%s want foundation", p.Title, p.Section, got)
		}
	}
	// Domain "…平台…业务总览" must not soft-match foundation via 平台+总览.
	got := InferTrack(WikiPage{Title: "知识管理平台业务总览", Section: "知识管理模块", Track: TrackFoundation})
	if got == TrackFoundation {
		t.Fatalf("domain 业务总览 must not be foundation, got %s", got)
	}
	// Framework boot pages stay technical even if previously stamped foundation.
	got2 := InferTrack(WikiPage{Title: "ExtJS 应用启动与 app.js 组织", Section: "前端架构设计", Track: TrackFoundation})
	if got2 == TrackFoundation {
		t.Fatalf("ExtJS boot page must not be foundation, got %s", got2)
	}
}


func TestRelatedCrossTrackEnglishIndexes(t *testing.T) {
	w := &Wiki{Pages: []WikiPage{
		{Title: "Order Management", Slug: "1", Section: "Orders", Track: TrackBusiness, Goal: "orders"},
		{Title: "API Documentation", Slug: "2", Section: "API Documentation", Track: TrackTechnical},
		{Title: "System Architecture", Slug: "3", Section: "System Architecture", Track: TrackTechnical},
		{Title: "Project Overview", Slug: "4", Section: "Project Overview", Track: TrackFoundation},
	}}
	// No shared tokens with business title → fallback to technical indexes (EN).
	rel := w.RelatedCrossTrack(w.Pages[0], 4)
	if len(rel) == 0 {
		t.Fatal("expected EN technical index fallback")
	}
	foundAPI := false
	for _, r := range rel {
		if r.Slug == "2" || r.Slug == "3" {
			foundAPI = true
		}
		if r.Track == TrackBusiness {
			t.Errorf("same-rail business returned: %q", r.Title)
		}
	}
	if !foundAPI {
		t.Fatalf("expected API/Architecture indexes, got %#v", rel)
	}
}

func TestRelatedCrossTrackTechToFoundation(t *testing.T) {
	w := &Wiki{Pages: []WikiPage{
		{Title: "WeirdUniqueXYZ", Slug: "1", Section: "API Documentation", Track: TrackTechnical},
		{Title: "Getting Started", Slug: "2", Section: "Getting Started", Track: TrackFoundation},
	}}
	rel := w.RelatedCrossTrack(w.Pages[0], 2)
	if len(rel) == 0 || rel[0].Slug != "2" {
		t.Fatalf("expected foundation fallback, got %#v", rel)
	}
}

func TestCatalogLinkTargetPrefersContentPath(t *testing.T) {
	p := WikiPage{Title: "客户基本信息", Slug: "50-50", ContentPath: "客户管理模块/客户基本信息.md"}
	got := catalogLinkTarget(p)
	if got != "客户管理模块/客户基本信息.md" {
		t.Fatalf("ContentPath preferred: got %q", got)
	}
	p2 := WikiPage{Title: "安全与访问控制", Slug: "144-144"}
	got2 := catalogLinkTarget(p2)
	if got2 != "安全与访问控制.md" {
		t.Fatalf("Title.md fallback: got %q", got2)
	}
	// Must not emit bare counter slug when title exists.
	if got2 == "144-144" {
		t.Fatal("must not use bare N-N slug when title present")
	}
}

func TestFormatCatalogUsesResolvableLinks(t *testing.T) {
	w := &Wiki{Pages: []WikiPage{
		{Title: "项目概述", Slug: "1-1", Section: "项目概述", Track: TrackFoundation, ContentPath: "项目概述/项目概述.md"},
		{Title: "客户基本信息", Slug: "50-50", Section: "客户管理模块", Track: TrackBusiness, ContentPath: "客户管理模块/客户基本信息.md"},
	}}
	out := w.FormatCatalog("50-50")
	if strings.Contains(out, "](50-50)") || strings.Contains(out, "](1-1)") {
		t.Fatalf("catalog must not use bare numeric slugs: %s", out)
	}
	if !strings.Contains(out, "](客户管理模块/客户基本信息.md)") {
		t.Fatalf("expected ContentPath link: %s", out)
	}
	if !strings.Contains(out, "[You are currently here]") {
		t.Fatal("current marker missing")
	}
}

