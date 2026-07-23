package web

import (
	"io"
	"net/http/httptest"
	"testing"

	"samwise/internal/store"
)

func TestPageParam(t *testing.T) {
	cases := map[string]int{"": 1, "0": 1, "-3": 1, "1": 1, "2": 2, "abc": 1, "5": 5}
	for q, want := range cases {
		r := httptest.NewRequest("GET", "/memory?page="+q, nil)
		if got := pageParam(r); got != want {
			t.Errorf("page=%q: got %d want %d", q, got, want)
		}
	}
}

func TestTrimPage(t *testing.T) {
	full := make([]store.SemanticMemory, memPageSize+1)
	rows, hasNext := trimPage(full)
	if len(rows) != memPageSize || !hasNext {
		t.Errorf("over-full page: got %d hasNext=%v", len(rows), hasNext)
	}
	short := make([]store.SemanticMemory, 3)
	rows, hasNext = trimPage(short)
	if len(rows) != 3 || hasNext {
		t.Errorf("short page: got %d hasNext=%v", len(rows), hasNext)
	}
}

// TestMemoryRowFragmentsRender smoke-tests the infinite-scroll fragments with
// the same data keys the handler passes.
func TestMemoryRowFragmentsRender(t *testing.T) {
	sem := pageData{
		"Agents":   []store.Agent{{ID: 2, Name: "Coach"}},
		"Semantic": []store.SemanticMemory{{ID: 1, AgentID: 2, Topic: "t", Kind: "fact", Content: "c", CreatedAt: "2026-06-24"}},
		"Back":     "/memory?topic=t",
	}
	for _, name := range []string{"memrows_topic", "memrows_index"} {
		if err := tmpl.ExecuteTemplate(io.Discard, name, sem); err != nil {
			t.Fatalf("%s render: %v", name, err)
		}
	}
	epi := pageData{
		"Episodic": []store.EpisodicMemory{{ID: 3, PeriodType: "day", PeriodDate: "2026-06-24", Content: "note"}},
		"Back":     "/memory?date=2026-06-24",
	}
	if err := tmpl.ExecuteTemplate(io.Discard, "memrows_date", epi); err != nil {
		t.Fatalf("memrows_date render: %v", err)
	}
}
