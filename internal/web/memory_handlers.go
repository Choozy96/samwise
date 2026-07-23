package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"samwise/internal/store"
)

// memPageSize is how many rows a topic/date drill-down view shows per page.
const memPageSize = 50

// handleMemory renders the memory browser with drill-down: an index of topics
// (semantic) and dates (episodic) — counted in SQL so they're accurate at any
// scale — plus paginated topic/date views and full-text search. Users only ever
// see their own memory.
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
	// Agents for the scope labels/dropdowns: agent_id 0 = user memory (shared).
	agents, err := s.db.ListAgents(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	agentNames := map[int64]string{}
	for _, a := range agents {
		agentNames[a.ID] = a.Name
	}

	data := pageData{
		"Title":      "Memory",
		"Topics":     topics,
		"Dates":      dates,
		"Agents":     agents,
		"AgentNames": agentNames,
		"Query":      q,
		"Topic":      topic,
		"Date":       date,
		"Page":       page,
		"PrevPage":   page - 1,
		"NextPage":   page + 1,
	}
	off := (page - 1) * memPageSize
	switch {
	case q != "":
		hits, herr := s.db.SearchMemory(ctx, u.ID, store.AllAgents, q, "", "", "", 60)
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
		data["Back"] = "/memory?topic=" + url.QueryEscape(topic)
		data["NextHref"] = "/memory?topic=" + url.QueryEscape(topic) + "&page=" + strconv.Itoa(page+1)
	case date != "":
		rows, herr := s.db.EpisodicByDate(ctx, u.ID, date, memPageSize+1, off)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		rows, hasNext := trimPageEpi(rows)
		data["View"], data["Episodic"], data["HasNext"] = "date", rows, hasNext
		data["Back"] = "/memory?date=" + url.QueryEscape(date)
		data["NextHref"] = "/memory?date=" + url.QueryEscape(date) + "&page=" + strconv.Itoa(page+1)
	default:
		recent, herr := s.db.ListSemanticPage(ctx, u.ID, store.AllAgents, memPageSize+1, off)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		recent, hasNext := trimPage(recent)
		data["View"], data["Semantic"], data["HasNext"] = "index", recent, hasNext
		data["Back"] = "/memory"
		data["NextHref"] = "/memory?page=" + strconv.Itoa(page+1)
	}
	s.render(w, r, "memory", data)
}

// handleMemoryRows serves ONE page of memory rows as an HTML fragment for the
// browser's infinite scroll: table rows for the topic/index views, note panels
// for the date view. X-Has-Next signals whether another page exists.
func (s *Server) handleMemoryRows(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	ctx := r.Context()
	view := r.URL.Query().Get("view")
	topic := strings.TrimSpace(r.URL.Query().Get("topic"))
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	page := pageParam(r)
	off := (page - 1) * memPageSize

	agents, err := s.db.ListAgents(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := pageData{"Agents": agents}
	name := ""
	hasNext := false
	switch view {
	case "topic":
		rows, herr := s.db.SemanticByTopic(ctx, u.ID, topic, memPageSize+1, off)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		rows, hasNext = trimPage(rows)
		data["Semantic"], data["Back"] = rows, "/memory?topic="+url.QueryEscape(topic)
		name = "memrows_topic"
	case "date":
		rows, herr := s.db.EpisodicByDate(ctx, u.ID, date, memPageSize+1, off)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		rows, hasNext = trimPageEpi(rows)
		data["Episodic"], data["Back"] = rows, "/memory?date="+url.QueryEscape(date)
		name = "memrows_date"
	default:
		rows, herr := s.db.ListSemanticPage(ctx, u.ID, store.AllAgents, memPageSize+1, off)
		if herr != nil {
			s.serverError(w, r, herr)
			return
		}
		rows, hasNext = trimPage(rows)
		data["Semantic"], data["Back"] = rows, "/memory"
		name = "memrows_index"
	}
	if hasNext {
		w.Header().Set("X-Has-Next", "1")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("memory rows fragment", "view", view, "err", err)
	}
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
	// scope: 0 = user memory (shared); a positive agent id scopes it to that agent.
	scope, _ := strconv.ParseInt(r.FormValue("scope"), 10, 64)
	if _, err := s.db.SaveSemantic(r.Context(), u.ID, scope, strings.TrimSpace(r.FormValue("topic")), kind, content, "portal"); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/memory", http.StatusSeeOther)
}

// handleMemoryScope moves a semantic memory between user-scope (scope=0) and a
// specific agent.
func (s *Server) handleMemoryScope(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err == nil {
		scope, _ := strconv.ParseInt(r.FormValue("scope"), 10, 64)
		if _, err = s.db.SetSemanticScope(r.Context(), u.ID, id, scope); err != nil {
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

// handleMemoryDelete removes one of the user's memories (semantic by default, or
// episodic when layer=episodic).
func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err == nil {
		if r.FormValue("layer") == "episodic" {
			_, err = s.db.ForgetEpisodic(r.Context(), u.ID, id)
		} else {
			_, err = s.db.ForgetSemantic(r.Context(), u.ID, store.AllAgents, id)
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

// handleAudit renders the user's own tool-call audit log.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	entries, err := s.db.ListAudit(r.Context(), u.ID, 200)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "audit", pageData{"Title": "Audit log", "Entries": entries})
}
