package main

import (
	"context"
	"flag"
	"fmt"

	"samwise/internal/auth"
)

// runSetPassword resets an existing user's password from the CLI. This is the
// headless recovery path: if the admin forgets their password on a box with no
// other way in, reset it here, then log in and change it from Settings.
//
//	samwise set-password --username alice --password 'news3cret!!'
func runSetPassword(args []string) error {
	fs := flag.NewFlagSet("set-password", flag.ContinueOnError)
	username := fs.String("username", "", "username of the account to reset")
	password := fs.String("password", "", "new password (min 8 chars)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" || *password == "" {
		return fmt.Errorf("both --username and --password are required")
	}
	if len(*password) < 8 {
		return fmt.Errorf("password must be ≥8 chars")
	}

	d, err := bootstrap()
	if err != nil {
		return err
	}
	defer d.db.Close()

	ctx := context.Background()
	u, err := d.db.GetUserByUsername(ctx, *username)
	if err != nil {
		return fmt.Errorf("user %q: %w", *username, err)
	}

	hash, err := auth.HashPassword(*password)
	if err != nil {
		return err
	}
	if err := d.db.UpdatePassword(ctx, u.ID, hash); err != nil {
		return err
	}
	d.log.Info("password reset via CLI", "user_id", u.ID, "username", u.Username)
	fmt.Printf("password reset for %q (id=%d)\n", u.Username, u.ID)
	return nil
}
