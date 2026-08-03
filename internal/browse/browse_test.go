package browse

import "testing"

func TestBuildCategoriesNumbersAndTrackOrder(t *testing.T) {
	pages := []pageInfo{
		{Title: "API 接口", Slug: "api", Section: "接口文档", Track: "technical"},
		{Title: "项目概述", Slug: "overview", Section: "项目概述", Track: "foundation"},
		{Title: "快速开始", Slug: "start", Section: "项目概述", Track: "foundation"},
		{Title: "客户档案", Slug: "cust", Section: "客户管理", Track: "business"},
	}
	cats := buildCategories(pages)
	if len(cats) != 3 {
		t.Fatalf("want 3 categories, got %d", len(cats))
	}
	// foundation first, then business, then technical
	if cats[0].Label != "项目概述" || cats[0].Index != 1 {
		t.Fatalf("first cat = %+v", cats[0])
	}
	if cats[0].Track != "foundation" {
		t.Fatalf("first track = %q", cats[0].Track)
	}
	if cats[1].Label != "客户管理" || cats[1].Track != "business" {
		t.Fatalf("second cat = %+v", cats[1])
	}
	if cats[2].Label != "接口文档" || cats[2].Track != "technical" {
		t.Fatalf("third cat = %+v", cats[2])
	}
	// global indices 01..N across all pages in display order
	var nums []int
	for _, c := range cats {
		for _, p := range c.Items {
			nums = append(nums, p.Index)
		}
	}
	if len(nums) != 4 || nums[0] != 1 || nums[1] != 2 || nums[2] != 3 || nums[3] != 4 {
		t.Fatalf("global indices = %v", nums)
	}
	// within-category indices
	if cats[0].Items[0].CatIndex != 1 || cats[0].Items[1].CatIndex != 2 {
		t.Fatalf("foundation cat indices: %+v %+v", cats[0].Items[0], cats[0].Items[1])
	}
	// titles preserved under reordered cats
	if cats[0].Items[0].Title != "项目概述" || cats[0].Items[1].Title != "快速开始" {
		t.Fatalf("foundation items: %+v", cats[0].Items)
	}
}
