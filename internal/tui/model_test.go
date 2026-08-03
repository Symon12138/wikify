package tui

import "testing"

func TestRecountIgnoresAutoRetryNoise(t *testing.T) {
	m := New()
	m.pages = []PageRow{
		{Slug: "a", Status: StatusPending},
		{Slug: "b", Status: StatusRunning},
		{Slug: "c", Status: StatusRetrying},
		{Slug: "d", Status: StatusDone},
		{Slug: "e", Status: StatusFailed},
		{Slug: "f", Status: StatusFailed},
	}
	// Simulate the old bug: many auto-retries would have decremented failCount.
	m.failCount = -999
	m.doneCount = -999
	m.recount()
	if m.doneCount != 1 {
		t.Fatalf("done=%d", m.doneCount)
	}
	if m.failCount != 2 {
		t.Fatalf("fail=%d", m.failCount)
	}

	// PageRetryingMsg path: recount after leaving failed.
	m.pages[4].Status = StatusRetrying
	m.recount()
	if m.failCount != 1 {
		t.Fatalf("after retry fail=%d", m.failCount)
	}
}

func TestProgressHeaderNonNegative(t *testing.T) {
	m := New()
	m.pages = make([]PageRow, 111)
	for i := range m.pages {
		m.pages[i] = PageRow{Slug: string(rune('a' + i%26)), Status: StatusFailed, Err: "boom"}
	}
	// Old code: 5 auto-retries × 111 pages each sent PageRetryingMsg → failCount -= 555, then +111 Failed → -444.
	m.failCount = -444
	m.recount()
	done, fail := m.countStatuses()
	if done != 0 || fail != 111 {
		t.Fatalf("done=%d fail=%d", done, fail)
	}
	if done+fail != 111 {
		t.Fatalf("counted=%d", done+fail)
	}
}
