package agent

import (
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/evidence"
	"github.com/Symon12138/wikify/internal/models"
)

func TestShouldSkipRepairPassInvalidCiteHardRule(t *testing.T) {
	page := &models.WikiPage{Title: "T", Track: models.TrackFoundation}
	longBody := strings.Repeat("prose ", 200) // > 400 runes

	twoInvalid := []string{
		invalidCitePrefix + " a/Missing.java (did you mean b/Missing.java?)",
		invalidCitePrefix + " c/Gone.go",
	}
	// >=2 invalid cites: never skipped, even for thin bodies / foundation pages.
	if shouldSkipRepairPass(page, "short", twoInvalid) {
		t.Fatal(">=2 invalid cites must force the repair pass on thin body")
	}
	if shouldSkipRepairPass(page, longBody, twoInvalid) {
		t.Fatal(">=2 invalid cites must force the repair pass")
	}

	// A single invalid cite is soft: alone it must not trigger repair.
	oneInvalid := []string{invalidCitePrefix + " a/Missing.java"}
	if !shouldSkipRepairPass(page, longBody, oneInvalid) {
		t.Fatal("a lone invalid cite is soft and should skip repair")
	}

	// Lone invalid cite + soft sequence hint on a foundation page: still skipped.
	softMix := []string{
		invalidCitePrefix + " a/Missing.java",
		"no sequenceDiagram detected (recommended for process pages)",
	}
	if !shouldSkipRepairPass(page, longBody, softMix) {
		t.Fatal("soft-only issue mix should skip repair on foundation pages")
	}

	// A real hard issue still triggers repair regardless of invalid-cite count.
	hard := []string{"missing TOC (## 目录)"}
	if shouldSkipRepairPass(page, longBody, hard) {
		t.Fatal("hard structural issue must trigger repair")
	}

	if !shouldSkipRepairPass(nil, longBody, hard) {
		t.Fatal("nil page must skip")
	}
	if !shouldSkipRepairPass(page, longBody, nil) {
		t.Fatal("no issues must skip")
	}
}

func TestDepthImproved(t *testing.T) {
	shallow := evidence.DepthReport{Sections: 2, DistinctCitePaths: 1, SectionSourceBlocks: 0}

	if !depthImproved(shallow, evidence.DepthReport{Sections: 3, DistinctCitePaths: 1, SectionSourceBlocks: 0}) {
		t.Fatal("more sections on a failing axis is an improvement")
	}
	if !depthImproved(shallow, evidence.DepthReport{Sections: 2, DistinctCitePaths: 2, SectionSourceBlocks: 0}) {
		t.Fatal("more distinct cite paths on a failing axis is an improvement")
	}
	if !depthImproved(shallow, evidence.DepthReport{Sections: 2, DistinctCitePaths: 1, SectionSourceBlocks: 1}) {
		t.Fatal("adding section sources on a failing axis is an improvement")
	}
	if depthImproved(shallow, shallow) {
		t.Fatal("identical report is not an improvement")
	}
	if depthImproved(shallow, evidence.DepthReport{Sections: 1, DistinctCitePaths: 1, SectionSourceBlocks: 0}) {
		t.Fatal("regression is not an improvement")
	}
	// Improvement on an already-passing axis does not count.
	passingSections := evidence.DepthReport{Sections: 5, DistinctCitePaths: 1, SectionSourceBlocks: 0}
	if depthImproved(passingSections, evidence.DepthReport{Sections: 9, DistinctCitePaths: 1, SectionSourceBlocks: 0}) {
		t.Fatal("growth on a passing axis must not count as improvement")
	}
}
