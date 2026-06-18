package main

import (
	"context"
	"flag"
	"fmt"

	"samwise/internal/auth"
)

// runCreateUser creates a portal user from the CLI. The first account created
// becomes admin regardless of the --admin flag. Useful for headless
// provisioning / the admin appendix install flow.
//
//	samwise create-user --username alice --password 's3cret!!' [--admin]
func runCreateUser(args []string) error {
	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	username := fs.String("username", "", "username (min 3 chars)")
	password := fs.String("password", "", "password (min 8 chars)")
	admin := fs.Bool("admin", false, "make this user an admin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" || *password == "" {
		return fmt.Errorf("both --username and --password are required")
	}
	if len(*username) < 3 || len(*password) < 8 {
		return fmt.Errorf("username must be ≥3 chars and password ≥8 chars")
	}

	d, err := bootstrap()
	if err != nil {
		return err
	}
	defer d.db.Close()

	ctx := context.Background()
	if existing, _ := d.db.GetUserByUsername(ctx, *username); existing != nil {
		return fmt.Errorf("username %q already exists", *username)
	}
	n, err := d.db.CountUsers(ctx)
	if err != nil {
		return err
	}
	isAdmin := *admin || n == 0

	hash, err := auth.HashPassword(*password)
	if err != nil {
		return err
	}
	id, err := d.db.CreateUser(ctx, *username, hash, isAdmin)
	if err != nil {
		return err
	}
	d.log.Info("user created", "user_id", id, "username", *username, "admin", isAdmin)
	fmt.Printf("created user %q (id=%d, admin=%v)\n", *username, id, isAdmin)
	return nil
}
