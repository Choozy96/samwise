package store

import (
	"context"
	"testing"
)

// TestEpisodicMemory covers saving, listing newest-period-first, and deletion.
func TestEpisodicMemory(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	uid, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"2026-06-01", "2026-06-03", "2026-06-02"} {
		if _, err := db.SaveEpisodic(ctx, uid, "day", d, "summary for "+d); err != nil {
			t.Fatal(err)
		}
	}

	epi, err := db.ListEpisodic(ctx, uid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(epi) != 3 {
		t.Fatalf("expected 3 episodic, got %d", len(epi))
	}
	// Newest period_date first.
	if epi[0].PeriodDate != "2026-06-03" || epi[2].PeriodDate != "2026-06-01" {
		t.Errorf("not ordered newest-first: %s … %s", epi[0].PeriodDate, epi[2].PeriodDate)
	}

	ok, err := db.ForgetEpisodic(ctx, uid, epi[0].ID)
	if err != nil || !ok {
		t.Fatalf("forget: ok=%v err=%v", ok, err)
	}
	epi, _ = db.ListEpisodic(ctx, uid, 10)
	if len(epi) != 2 {
		t.Errorf("expected 2 after delete, got %d", len(epi))
	}

	// Cross-user isolation.
	other, _ := db.CreateUser(ctx, "bob", "h", false)
	if e, _ := db.ListEpisodic(ctx, other, 10); len(e) != 0 {
		t.Errorf("bob should see no episodic memory, got %d", len(e))
	}
}

// TestMemoryIndexesAndPagination covers the aggregate index queries and the
// paginated drill-down used by the Memory browser.
func TestMemoryIndexesAndPagination(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	uid, _ := db.CreateUser(ctx, "alice", "h", true)

	for i := 0; i < 5; i++ {
		_, _ = db.SaveSemantic(ctx, uid, 0, "work", "fact", "w"+string(rune('a'+i)), "test")
	}
	_, _ = db.SaveSemantic(ctx, uid, 0, "health", "fact", "h1", "test")
	_, _ = db.SaveSemantic(ctx, uid, 0, "", "fact", "untagged", "test") // excluded from topic index

	tc, err := db.TopicCounts(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(tc) != 2 || tc[0].Topic != "health" || tc[1].Topic != "work" || tc[1].Count != 5 {
		t.Fatalf("topic counts wrong: %+v", tc)
	}

	// Pagination: page 1 of 2 (size 2) returns 2 of the 5 work facts.
	p1, _ := db.SemanticByTopic(ctx, uid, "work", 2, 0)
	p3, _ := db.SemanticByTopic(ctx, uid, "work", 2, 4)
	if len(p1) != 2 || len(p3) != 1 {
		t.Errorf("pagination sizes wrong: p1=%d p3=%d", len(p1), len(p3))
	}

	for _, d := range []string{"2026-06-01", "2026-06-02", "2026-06-02"} {
		_, _ = db.SaveEpisodic(ctx, uid, "day", d, "x")
	}
	dc, _ := db.EpisodicDateCounts(ctx, uid)
	if len(dc) != 2 || dc[0].Date != "2026-06-02" || dc[0].Count != 2 {
		t.Errorf("date counts wrong: %+v", dc)
	}
	if e, _ := db.EpisodicByDate(ctx, uid, "2026-06-02", 1, 0); len(e) != 1 {
		t.Errorf("episodic page wrong: %d", len(e))
	}
}

// TestUpsertEpisodicAndRecent covers the in-place day-note update + recency load.
func TestUpsertEpisodicAndRecent(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	uid, _ := db.CreateUser(ctx, "alice", "h", true)

	// Upsert twice for the same day → one row, latest content, FTS-searchable.
	if err := db.UpsertEpisodic(ctx, uid, "day", "2026-06-14", "first version"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertEpisodic(ctx, uid, "day", "2026-06-14", "second version groceries"); err != nil {
		t.Fatal(err)
	}
	all, _ := db.ListEpisodic(ctx, uid, 10)
	if len(all) != 1 || all[0].Content != "second version groceries" {
		t.Fatalf("upsert didn't replace in place: %+v", all)
	}
	hits, _ := db.SearchMemory(ctx, uid, AllAgents, "groceries", "", "", "", 5)
	if len(hits) != 1 {
		t.Errorf("FTS not updated after upsert: %d hits", len(hits))
	}

	// RecentEpisodic includes on/after the cutoff, excludes older.
	_ = db.UpsertEpisodic(ctx, uid, "day", "2026-06-10", "old day")
	recent, _ := db.RecentEpisodic(ctx, uid, "2026-06-13")
	if len(recent) != 1 || recent[0].PeriodDate != "2026-06-14" {
		t.Errorf("recent window wrong: %+v", recent)
	}
}
