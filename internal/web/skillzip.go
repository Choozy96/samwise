package web

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits guard against zip bombs and abuse.
const (
	maxSkillFiles      = 500
	maxSkillTotalBytes = 50 << 20 // 50 MiB uncompressed
	maxSkillFileBytes  = 20 << 20 // 20 MiB per file
)

// findSkillMD returns the content of SKILL.md inside the zip (after stripping a
// single common top-level folder, as `zip -r` produces).
func findSkillMD(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("not a valid zip: %w", err)
	}
	top := commonTopDir(zr.File)
	for _, f := range zr.File {
		name := stripTop(f.Name, top)
		if strings.EqualFold(name, "SKILL.md") {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			b, err := io.ReadAll(io.LimitReader(rc, maxSkillFileBytes))
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
	}
	return "", errors.New("no SKILL.md found at the root of the zip")
}

// extractSkillZip extracts the bundle into destDir (replacing any existing
// contents), guarding against zip-slip, oversized archives, and symlinks.
// Returns the number of files written.
func extractSkillZip(data []byte, destDir string) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("not a valid zip: %w", err)
	}
	if err := os.RemoveAll(destDir); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return 0, err
	}
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return 0, err
	}

	top := commonTopDir(zr.File)
	var written int
	var total int64
	for _, f := range zr.File {
		rel := stripTop(f.Name, top)
		if rel == "" {
			continue
		}
		// Reject absolute paths and traversal.
		clean := path.Clean("/" + strings.ReplaceAll(rel, `\`, "/"))[1:]
		if clean == "" || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return written, fmt.Errorf("unsafe path in zip: %q", f.Name)
		}
		target := filepath.Join(destDir, filepath.FromSlash(clean))
		if abs, _ := filepath.Abs(target); !strings.HasPrefix(abs, absDest+string(os.PathSeparator)) && abs != absDest {
			return written, fmt.Errorf("path escapes destination: %q", f.Name)
		}
		info := f.FileInfo()
		if info.IsDir() {
			_ = os.MkdirAll(target, 0o700)
			continue
		}
		if !info.Mode().IsRegular() { // skip symlinks/devices
			continue
		}
		if written >= maxSkillFiles {
			return written, fmt.Errorf("too many files (max %d)", maxSkillFiles)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return written, err
		}
		n, err := writeZipEntry(f, target, maxSkillTotalBytes-total)
		if err != nil {
			return written, err
		}
		total += n
		written++
		if total > maxSkillTotalBytes {
			return written, fmt.Errorf("archive too large (max %d bytes)", maxSkillTotalBytes)
		}
	}
	return written, nil
}

func writeZipEntry(f *zip.File, target string, remaining int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	limit := remaining
	if limit > maxSkillFileBytes {
		limit = maxSkillFileBytes
	}
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, fmt.Errorf("file %q exceeds size limit", f.Name)
	}
	return n, nil
}

// commonTopDir returns the single shared top-level folder of the entries, or ""
// if they don't all share one.
func commonTopDir(files []*zip.File) string {
	var top string
	for _, f := range files {
		name := strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), "./")
		if name == "" {
			continue
		}
		seg := name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			seg = name[:i]
		} else {
			return "" // a file sits at the root → no common top dir
		}
		if top == "" {
			top = seg
		} else if top != seg {
			return ""
		}
	}
	return top
}

func stripTop(name, top string) string {
	name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./")
	if top != "" {
		name = strings.TrimPrefix(name, top+"/")
	}
	return name
}

// parseFrontMatter extracts name/description from a SKILL.md YAML front-matter
// block and returns the body (content after the block). Minimal parser — no YAML
// dependency; handles `key: value` lines between leading `---` fences.
func parseFrontMatter(md string) (name, description, body string) {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(md, "---\n") {
		return "", "", md
	}
	rest := md[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", md
	}
	fm := rest[:end]
	body = strings.TrimLeft(rest[end+len("\n---"):], "-\n")
	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		switch key {
		case "name":
			name = val
		case "description":
			description = val
		}
	}
	return name, description, strings.TrimSpace(body)
}
