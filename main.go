// Command samwise is the personal AI assistant orchestrator.
//
// It is a single binary with subcommands:
//
//	serve        run the orchestrator (web portal, scheduler, run dispatch) — default
//	migrate      apply database migrations and exit
//	create-user  create a portal user (first user becomes admin)
//	set-password reset a user's password (headless recovery)
//	mcp          run the core MCP server over stdio, scoped to a run context
//	             (spawned by the runtime adapters, not invoked by hand)
//	version      print version and exit
//
// The mcp subcommand exists so the orchestrator can hand the harness a
// --mcp-config that points back at this same binary, bound to a user_id/run_id
// the orchestrator chose — the model never selects whose data it touches.
package main

import (
	"fmt"
	"os"

	// Embed the IANA timezone database so time.LoadLocation works on Windows
	// and in slim containers that lack system zoneinfo. Timezones are core to
	// the scheduler, so this must always resolve.
	_ "time/tzdata"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "migrate":
		err = runMigrate(args)
	case "create-user":
		err = runCreateUser(args)
	case "set-password":
		err = runSetPassword(args)
	case "mcp":
		err = runMCP(args)
	case "version", "--version", "-v":
		fmt.Println("samwise", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `samwise — personal AI assistant orchestrator

usage:
  samwise [command] [flags]

commands:
  serve         run the orchestrator (default)
  migrate       apply database migrations and exit
  create-user   create a portal user (first user is admin)
  set-password  reset a user's password by username (headless recovery)
  mcp           core MCP server over stdio (internal; spawned by runtimes)
  version       print version
`)
}
