package main

import (
	"log/slog"

	"samwise/internal/applog"
	"samwise/internal/config"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// deps bundles the shared dependencies most subcommands need. Built once by
// bootstrap and passed down explicitly (no global state beyond slog's default).
type deps struct {
	cfg *config.Config
	log *slog.Logger
	db  *store.DB
	box *secretbox.Box
}

// bootstrap loads config, installs logging, opens the DB, and runs migrations.
// The caller owns closing deps.db.
func bootstrap() (*deps, error) {
	cfg, err := config.Load(".env")
	if err != nil {
		return nil, err
	}
	log := applog.New(cfg.LogLevel)

	box, err := secretbox.New(cfg.MasterKey)
	if err != nil {
		return nil, err
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &deps{cfg: cfg, log: log, db: db, box: box}, nil
}
