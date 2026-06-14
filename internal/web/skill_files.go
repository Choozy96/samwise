package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"samwise/internal/store"
)

const maxSkillFileView = 256 << 10 // show files up to 256 KiB

// skillView is a skill plus its bundle's file listing (for the Extensions page).
type skillView struct {
	store.Skill
	Bundle []bundleFile
}

// bundleFile is one file inside a skill bundle, with a display-friendly size.
type bundleFile struct {
	Path string // relative to the bundle dir, forward-slashed
	Size string
}

var skipBundleDirs = map[string]bool{".venv": true, "__pycache__": true, ".git": true}

// skillBundleFiles walks a skill's bundle dir and returns its files (relative
// paths), skipping virtualenvs and caches. Empty if the skill has no bundle.
func (s *Server) skillBundleFiles(userID int64, sk store.Skill) []bundleFile {
	if !sk.HasBundle {
		return nil
	}
	dir := s.orch.SkillBundleDir(userID, sk.Name)
	var out []bundleFile
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipBundleDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		size := "?"
		if info, ierr := d.Info(); ierr == nil {
			size = humanSize(info.Size())
		}
		out = append(out, bundleFile{Path: filepath.ToSlash(rel), Size: size})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// handleSkillFile serves one file from a user-owned skill bundle, read-only, with
// path-traversal and size guards. Binary files are reported, not dumped.
func (s *Server) handleSkillFile(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	sk, err := s.db.GetSkill(r.Context(), id)
	if err != nil || sk == nil || !sk.HasBundle || sk.UserID != u.ID {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}
	dir := s.orch.SkillBundleDir(u.ID, sk.Name)
	target, ok := safeBundlePath(dir, r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	data := pageData{"Title": "Skill file", "SkillName": sk.Name, "SkillID": id, "Path": r.URL.Query().Get("path")}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.Size() > maxSkillFileView {
		data["Note"] = fmt.Sprintf("File is %s — too large to display here.", humanSize(info.Size()))
		s.render(w, r, "skillfile", data)
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
	}
	s.render(w, r, "skillfile", data)
}

// safeBundlePath resolves a user-supplied relative path under dir, rejecting any
// result that escapes dir (path traversal). Cleaning against a leading "/" first
// collapses ../ sequences so they can't climb above the root.
func safeBundlePath(dir, rel string) (string, bool) {
	cleaned := strings.TrimPrefix(filepath.Clean("/"+rel), "/")
	target := filepath.Join(dir, cleaned)
	if target != dir && !strings.HasPrefix(target, dir+string(os.PathSeparator)) {
		return "", false
	}
	return target, true
}

func hasNullByte(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
