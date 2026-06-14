package web

import (
	"net/http"
	"strconv"
	"strings"

	"samwise/internal/store"
)

// memPageSize is how many rows a topic/date drill-down view shows per page.
const memPageSize = 50

// handleMemory renders the memory browser with drill-down: an index of topics
// (semantic) and dates (episodic) — counted in SQL so they're accurate at any
// scale — plus paginated topic/date views and full-text search. Users only ever
// see their own memory (spec §6, §9, §12).
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	ctx := r.Context()

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	topic := strings.TrimSpace(r.URL.Query().Get("topic"))
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	page := pageParam(r)

	topics, err := s.db.TopicCounts(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	dates, err := s.db.EpisodicDateCounts(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	data := pageData{
		"Title":    "Memory",
		"Topics":   topics,
		"Dates":    dates,
		"Query":    q,
		"Topic":    topic,
		"Date":     date,
		"Page":     page,
		"PrevPage": page - 1,
		"NextPage": page + 1,
	}
	off := (page - 1) * memPageSize
	switch {
	case q != "":
		hits, herr := s.db.SearchMemory(ctx, u.ID, q, "", "", "", 60)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		data["View"], data["Hits"] = "search", hits
	case topic != "":
		rows, herr := s.db.SemanticByTopic(ctx, u.ID, topic, memPageSize+1, off)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		rows, hasNext := trimPage(rows)
		data["View"], data["Semantic"], data["HasNext"] = "topic", rows, hasNext
	case date != "":
		rows, herr := s.db.EpisodicByDate(ctx, u.ID, date, memPageSize+1, off)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		rows, hasNext := trimPageEpi(rows)
		data["View"], data["Episodic"], data["HasNext"] = "date", rows, hasNext
	default:
		recent, herr := s.db.ListSemantic(ctx, u.ID, 25)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		data["View"], data["Semantic"] = "index", recent
	}
	s.render(w, r, "memory", data)
}

func pageParam(r *http.Request) int {
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 1 {
		return p
	}
	return 1
}

// trimPage / trimPageEpi take a slice fetched with one extra row and report
// whether a next page exists, returning just the page's rows.
func trimPage(rows []store.SemanticMemory) ([]store.SemanticMemory, bool) {
	if len(rows) > memPageSize {
		return rows[:memPageSize], true
	}
	return rows, false
}
func trimPageEpi(rows []store.EpisodicMemory) ([]store.EpisodicMemory, bool) {
	if len(rows) > memPageSize {
		return rows[:memPageSize], true
	}
	return rows, false
}

// handleMemoryAdd saves a semantic memory entered in the portal.
func (s *Server) handleMemoryAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" {
		http.Redirect(w, r, "/memory", http.StatusSeeOther)
		return
	}
	kind := r.FormValue("kind")
	switch kind {
	case "fact", "preference", "event":
	default:
		kind = "fact"
	}
	if _, err := s.db.SaveSemantic(r.Context(), u.ID, strings.TrimSpace(r.FormValue("topic")), kind, content, "portal"); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/memory", http.StatusSeeOther)
}

// handleMemoryDelete removes one of the user's memories (semantic by default, or
// episodic when layer=episodic).
func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err == nil {
		if r.FormValue("layer") == "episodic" {
			_, err = s.db.ForgetEpisodic(r.Context(), u.ID, id)
		} else {
			_, err = s.db.ForgetSemantic(r.Context(), u.ID, id)
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	dest := "/memory"
	if back := r.FormValue("back"); back != "" {
		dest = back
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// handleAudit renders the user's own tool-call audit log (spec §10.5, §12).
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	entries, err := s.db.ListAudit(r.Context(), u.ID, 200)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "audit", pageData{"Title": "Audit log", "Entries": entries})
}
