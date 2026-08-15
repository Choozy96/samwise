package web

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxUploadBytes = 25 << 20 // per uploaded file

// hiddenWorkspaceDirs are workspace subtrees the Files browser never shows,
// serves, or writes into: .claude holds the claude credential symlink + per-run
// transcripts (sensitive, internal), the rest is dependency/cache noise.
var hiddenWorkspaceDirs = map[string]bool{".claude": true, ".venv": true, "__pycache__": true, ".git": true}

// wsEntry is one row (file or directory) in the Files browser.
type wsEntry struct {
	Name     string
	Rel      string // forward-slashed path relative to the workspace root
	Dir      bool
	Size     string
	Modified string
}

// crumb is one breadcrumb segment.
type crumb struct {
	Name string
	Rel  string
}

// workspacePath resolves a user-supplied relative path inside the user's
// workspace, rejecting traversal and anything under a hidden dir.
func workspacePath(root, rel string) (string, bool) {
	if hiddenWorkspacePath(rel) {
		return "", false
	}
	return safeBundlePath(root, rel)
}

// hiddenWorkspacePath reports whether any segment of a relative path is a
// hidden workspace dir (so no read or write can reach into .claude etc.).
func hiddenWorkspacePath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if hiddenWorkspaceDirs[seg] {
			return true
		}
	}
	return false
}

// validEntryName accepts a single new file/folder name: one path segment, not a
// traversal token, not a hidden name.
func validEntryName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return !hiddenWorkspaceDirs[name]
}

// filesURL builds the browser URL for a directory, with an optional flash key.
func filesURL(rel, msg string) string {
	u := "/files"
	q := url.Values{}
	if rel != "" && rel != "." {
		q.Set("path", filepath.ToSlash(rel))
	}
	if msg != "" {
		q.Set("msg", msg)
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

// handleFiles renders one directory of the user's workspace: folders first,
// with breadcrumbs, and forms to upload / create a folder here.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	rel := strings.Trim(filepath.ToSlash(r.URL.Query().Get("path")), "/")

	target, ok := workspacePath(root, rel)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		// A file path: send them to the viewer instead of erroring.
		http.Redirect(w, r, "/files/view?path="+url.QueryEscape(rel), http.StatusSeeOther)
		return
	}

	var entries []wsEntry
	dirents, err := os.ReadDir(target) // a missing dir (fresh workspace) lists as empty
	if err == nil {
		for _, d := range dirents {
			if hiddenWorkspaceDirs[d.Name()] {
				continue
			}
			e := wsEntry{Name: d.Name(), Rel: path.Join(rel, d.Name()), Dir: d.IsDir(), Size: "—"}
			if info, ierr := d.Info(); ierr == nil {
				e.Modified = info.ModTime().Format("2006-01-02 15:04")
				if !d.IsDir() {
					e.Size = humanSize(info.Size())
				}
			}
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir // folders first
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	// Breadcrumbs: Files / seg1 / seg2 …
	var crumbs []crumb
	if rel != "" {
		acc := ""
		for _, seg := range strings.Split(rel, "/") {
			acc = path.Join(acc, seg)
			crumbs = append(crumbs, crumb{Name: seg, Rel: acc})
		}
	}

	data := pageData{
		"Title": "Files", "Entries": entries, "Rel": rel, "Crumbs": crumbs,
	}
	if rel != "" {
		data["ParentRel"] = path.Dir(rel)
		if data["ParentRel"] == "." {
			data["ParentRel"] = ""
		}
	}
	switch r.URL.Query().Get("msg") {
	case "uploaded":
		data["Flash"], data["FlashKind"] = "Uploaded.", "ok"
	case "mkdir":
		data["Flash"], data["FlashKind"] = "Folder created.", "ok"
	case "renamed":
		data["Flash"], data["FlashKind"] = "Renamed.", "ok"
	case "deleted":
		data["Flash"], data["FlashKind"] = "Deleted.", "ok"
	case "saved":
		data["Flash"], data["FlashKind"] = "Saved.", "ok"
	case "badname":
		data["Flash"], data["FlashKind"] = "That name isn't allowed (one plain name, no slashes).", "error"
	case "badpath":
		data["Flash"], data["FlashKind"] = "That path isn't allowed.", "error"
	case "toobig":
		data["Flash"], data["FlashKind"] = fmt.Sprintf("File too large (max %s per upload).", humanSize(maxUploadBytes)), "error"
	case "err":
		data["Flash"], data["FlashKind"] = "The operation failed — check the name and try again.", "error"
	}
	s.render(w, r, "files", data)
}

// handleFileView serves one workspace file: shown read-only, and — when it's a
// text file within the edit cap — with an editable save form.
func (s *Server) handleFileView(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	rel := strings.Trim(filepath.ToSlash(r.URL.Query().Get("path")), "/")
	target, ok := workspacePath(root, rel)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	data := pageData{"Title": "Workspace file", "Path": rel, "DirRel": dir}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.Size() > maxSkillFileView {
		data["Note"] = fmt.Sprintf("File is %s — too large to display or edit here.", humanSize(info.Size()))
		s.render(w, r, "wsfile", data)
		return
	}
	b, err := os.ReadFile(target)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !utf8.Valid(b) || hasNullByte(b) {
		data["Note"] = fmt.Sprintf("Binary file (%s) — not shown.", humanSize(info.Size()))
	} else {
		data["Content"] = string(b)
		data["Editable"] = true
	}
	s.render(w, r, "wsfile", data)
}

// handleFileDownload serves a workspace file as a download (Content-Disposition
// attachment) — for binaries like .xlsx and for grabbing a file the assistant
// produced. Same path guards as the viewer.
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	rel := strings.Trim(filepath.ToSlash(r.URL.Query().Get("path")), "/")
	target, ok := workspacePath(root, rel)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	name := filepath.Base(target)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, target)
}

// handleFileSave overwrites an existing text file with edited content from the
// viewer. Textarea CRLFs are normalized so scripts keep working.
func (s *Server) handleFileSave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	rel := strings.Trim(filepath.ToSlash(r.FormValue("path")), "/")
	target, ok := workspacePath(root, rel)
	if !ok {
		http.Redirect(w, r, filesURL("", "badpath"), http.StatusSeeOther)
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.Redirect(w, r, filesURL("", "badpath"), http.StatusSeeOther)
		return
	}
	content := strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n")
	if len(content) > maxSkillFileView {
		http.Redirect(w, r, filesURL(path.Dir(rel), "toobig"), http.StatusSeeOther)
		return
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "file", "save", rel, "ok")
	http.Redirect(w, r, "/files/view?path="+url.QueryEscape(rel), http.StatusSeeOther)
}

// handleFileUpload saves uploaded file(s) into the current directory.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	if err := r.ParseMultipartForm(maxUploadBytes + (1 << 20)); err != nil {
		http.Redirect(w, r, filesURL("", "toobig"), http.StatusSeeOther)
		return
	}
	dir := strings.Trim(filepath.ToSlash(r.FormValue("dir")), "/")
	if _, ok := workspacePath(root, dir); !ok {
		http.Redirect(w, r, filesURL("", "badpath"), http.StatusSeeOther)
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Redirect(w, r, filesURL(dir, "err"), http.StatusSeeOther)
		return
	}
	for _, fh := range files {
		name := filepath.Base(filepath.ToSlash(fh.Filename))
		if !validEntryName(name) {
			http.Redirect(w, r, filesURL(dir, "badname"), http.StatusSeeOther)
			return
		}
		if fh.Size > maxUploadBytes {
			http.Redirect(w, r, filesURL(dir, "toobig"), http.StatusSeeOther)
			return
		}
		dst, ok := workspacePath(root, path.Join(dir, name))
		if !ok {
			http.Redirect(w, r, filesURL(dir, "badpath"), http.StatusSeeOther)
			return
		}
		src, err := fh.Open()
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			src.Close()
			s.serverError(w, r, err)
			return
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			src.Close()
			s.serverError(w, r, err)
			return
		}
		_, cerr := io.Copy(out, io.LimitReader(src, maxUploadBytes))
		src.Close()
		if err := out.Close(); err == nil {
			err = cerr
		}
		if cerr != nil {
			s.serverError(w, r, cerr)
			return
		}
		_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "file", "upload", path.Join(dir, name), "ok")
	}
	http.Redirect(w, r, filesURL(dir, "uploaded"), http.StatusSeeOther)
}

// handleFileMkdir creates a folder in the current directory.
func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	dir := strings.Trim(filepath.ToSlash(r.FormValue("dir")), "/")
	name := strings.TrimSpace(r.FormValue("name"))
	if !validEntryName(name) {
		http.Redirect(w, r, filesURL(dir, "badname"), http.StatusSeeOther)
		return
	}
	target, ok := workspacePath(root, path.Join(dir, name))
	if !ok {
		http.Redirect(w, r, filesURL(dir, "badpath"), http.StatusSeeOther)
		return
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "file", "mkdir", path.Join(dir, name), "ok")
	http.Redirect(w, r, filesURL(dir, "mkdir"), http.StatusSeeOther)
}

// handleFileRename renames a file or folder within its directory.
func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	rel := strings.Trim(filepath.ToSlash(r.FormValue("path")), "/")
	newName := strings.TrimSpace(r.FormValue("name"))
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	if rel == "" || !validEntryName(newName) {
		http.Redirect(w, r, filesURL(dir, "badname"), http.StatusSeeOther)
		return
	}
	from, ok1 := workspacePath(root, rel)
	to, ok2 := workspacePath(root, path.Join(dir, newName))
	if !ok1 || !ok2 {
		http.Redirect(w, r, filesURL(dir, "badpath"), http.StatusSeeOther)
		return
	}
	if _, err := os.Stat(to); err == nil {
		http.Redirect(w, r, filesURL(dir, "err"), http.StatusSeeOther) // don't clobber
		return
	}
	if err := os.Rename(from, to); err != nil {
		http.Redirect(w, r, filesURL(dir, "err"), http.StatusSeeOther)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "file", "rename", rel+" → "+newName, "ok")
	http.Redirect(w, r, filesURL(dir, "renamed"), http.StatusSeeOther)
}

// handleFileDelete removes a file, or a folder recursively.
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	root := s.orch.WorkspaceDir(u.ID)
	rel := strings.Trim(filepath.ToSlash(r.FormValue("path")), "/")
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	if rel == "" { // never delete the workspace root
		http.Redirect(w, r, filesURL("", "badpath"), http.StatusSeeOther)
		return
	}
	target, ok := workspacePath(root, rel)
	if !ok {
		http.Redirect(w, r, filesURL(dir, "badpath"), http.StatusSeeOther)
		return
	}
	if err := os.RemoveAll(target); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "file", "delete", rel, "ok")
	http.Redirect(w, r, filesURL(dir, "deleted"), http.StatusSeeOther)
}
