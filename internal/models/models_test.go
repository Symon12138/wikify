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
		{WikiPage{Section: "客户管理模块"}, TrackBusiness},
		{WikiPage{Section: "接口文档"}, TrackTechnical},
		{WikiPage{Section: "系统架构设计"}, TrackTechnical},
		{WikiPage{Title: "客户档案管理", Section: "业务"}, TrackBusiness},
	}
	for _, c := range cases {
		got := InferTrack(c.p)
		if got != c.want {
			t.Errorf("InferTrack(%+v)=%s want %s", c.p, got, c.want)
		}
	}
}
