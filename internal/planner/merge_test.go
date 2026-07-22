package planner

import (
	"testing"

	"github.com/symon/wikify/internal/models"
)

func TestMergePlannerFieldsExact(t *testing.T) {
	seed := &models.Wiki{Pages: []models.WikiPage{{
		Title: "项目概述", Section: "项目概述", Goal: "说明项目", Parent: "",
		ContentPath: "项目概述.md", DescriptionSlug: "overview",
	}}}
	llm := &models.Wiki{Pages: []models.WikiPage{{
		Title: "项目概述", Section: "项目概述", Slug: "1-x",
	}}}
	out := MergePlannerFields(llm, seed)
	if out.Pages[0].Goal != "说明项目" {
		t.Fatalf("goal: %+v", out.Pages[0])
	}
	if out.Pages[0].ContentPath != "项目概述.md" {
		t.Fatalf("path: %s", out.Pages[0].ContentPath)
	}
}

func TestMergePlannerFieldsFuzzyTitle(t *testing.T) {
	seed := &models.Wiki{Pages: []models.WikiPage{{
		Title: "整体架构设计", Section: "系统架构设计", Parent: "系统架构设计",
		Goal: "架构", ContentPath: "系统架构设计/整体架构设计.md", DescriptionSlug: "arch",
	}}}
	// LLM slightly rewrote the title
	llm := &models.Wiki{Pages: []models.WikiPage{{
		Title: "整体架构", Section: "系统架构设计", Slug: "2-arch",
	}}}
	out := MergePlannerFields(llm, seed)
	p := out.Pages[0]
	if p.Goal != "架构" {
		t.Fatalf("expected goal from seed, got %+v", p)
	}
	if p.ContentPath != "系统架构设计/整体架构设计.md" {
		t.Fatalf("expected multilevel path, got %s", p.ContentPath)
	}
	if p.Parent != "系统架构设计" {
		t.Fatalf("parent: %s", p.Parent)
	}
}

func TestMergePlannerFieldsReconstructPath(t *testing.T) {
	llm := &models.Wiki{Pages: []models.WikiPage{{
		Title: "支付回调", Section: "核心模块", Parent: "核心模块", Slug: "3-x",
	}}}
	out := MergePlannerFields(llm, &models.Wiki{})
	if out.Pages[0].ContentPath != "核心模块/支付回调.md" {
		t.Fatalf("path: %s", out.Pages[0].ContentPath)
	}
}

func TestNormalizeTitle(t *testing.T) {
	a := normalizeTitle("  整体架构设计！ ")
	b := normalizeTitle("整体架构设计")
	if a != b {
		t.Fatalf("%q vs %q", a, b)
	}
}
