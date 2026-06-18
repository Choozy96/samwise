package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// backupKeep is how many nightly snapshots to retain.
const backupKeep = 7

// Backup writes a consistent SQLite snapshot via VACUUM INTO and prunes old
// snapshots. The snapshot is a single file safe to copy off-box;
// because portal-stored secrets are encrypted at rest, the snapshot is too.
//
// Encrypting the archive itself (age/AES-GCM) and off-box copy are noted seams
// — the local rotated snapshot is the MVP.
func (o *Orchestrator) Backup(ctx context.Context, now time.Time) error {
	dir := filepath.Join(filepath.Dir(o.cfg.DBPath), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("backup: mkdir: %w", err)
	}
	dest := filepath.Join(dir, fmt.Sprintf("app-%s.db", now.UTC().Format("20060102-150405")))

	// VACUUM INTO produces a clean, consistent copy without locking writers out.
	if _, err := o.db.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("backup: vacuum into: %w", err)
	}
	o.log.Info("backup written", "path", dest)
	o.pruneBackups(dir)
	return nil
}

func (o *Orchestrator) pruneBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".db" {
			names = append(names, e.Name())
		}
	}
	if len(names) <= backupKeep {
		return
	}
	sort.Strings(names) // timestamped names sort chronologically
	for _, old := range names[:len(names)-backupKeep] {
		if err := os.Remove(filepath.Join(dir, old)); err != nil {
			o.log.Warn("backup prune failed", "file", old, "err", err)
		}
	}
}
