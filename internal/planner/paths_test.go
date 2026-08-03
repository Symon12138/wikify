package planner

import (
	"strings"
	"testing"

	"github.com/JSHurt/wikify/internal/models"
)

func TestApplyHierarchyPaths(t *testing.T) {
	wiki := &models.Wiki{Pages: []models.WikiPage{
		{Title: "项目概述", Section: "项目概述", Slug: "1-a"},
		{Title: "整体架构设计", Section: "系统架构设计", Slug: "2-b"},
		{Title: "支付回调", Section: "核心模块详解", Group: "支付域", Slug: "3-c"},
	}}
	ApplyHierarchyPaths(wiki)

	if wiki.Pages[0].ContentPath != "项目概述.md" {
		t.Fatalf("root page path: %s", wiki.Pages[0].ContentPath)
	}
	if wiki.Pages[1].ContentPath != "系统架构设计/整体架构设计.md" {
		t.Fatalf("section path: %s", wiki.Pages[1].ContentPath)
	}
	if wiki.Pages[1].Parent != "系统架构设计" {
		t.Fatalf("parent: %s", wiki.Pages[1].Parent)
	}
	if wiki.Pages[2].ContentPath != "核心模块详解/支付域/支付回调.md" {
		t.Fatalf("group path: %s", wiki.Pages[2].ContentPath)
	}
	if wiki.Pages[2].Parent != "支付域" {
		t.Fatalf("group parent: %s", wiki.Pages[2].Parent)
	}
}

func TestFormatInventoryHint(t *testing.T) {
	if FormatInventoryHint(nil, 10) == "" {
		t.Fatal("expected placeholder")
	}
	wiki := &models.Wiki{Pages: []models.WikiPage{{Title: "A", Section: "S"}}}
	h := FormatInventoryHint(wiki, 10)
	if !strings.Contains(h, "A") || !strings.Contains(h, "S") {
		t.Fatalf("hint: %s", h)
	}
}
