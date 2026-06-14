package web

import (
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
