package main

// runMigrate applies database migrations and exits. bootstrap already runs
// migrations, so this is mostly an explicit, log-and-exit entry point for ops.
func runMigrate(_ []string) error {
	d, err := bootstrap()
	if err != nil {
		return err
	}
	defer d.db.Close()
	d.log.Info("migrations up to date", "db", d.cfg.DBPath)
	return nil
}
