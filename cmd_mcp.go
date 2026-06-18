package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"samwise/internal/mcpserver"
)

// runMCP runs the core MCP server over stdio, bound to the user_id/run_id the
// orchestrator supplies at spawn time. It is invoked by the runtime
// adapters via a generated --mcp-config, not by hand.
//
//	samwise mcp --db <path> --user-id <N> --run-id <M>
//
// stdout is the MCP protocol channel; nothing else may write to it.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	dbPath := fs.String("db", "", "path to the SQLite database")
	userID := fs.Int64("user-id", 0, "run-context user id")
	runID := fs.Int64("run-id", 0, "run-context run id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *userID == 0 {
		return fmt.Errorf("mcp: --db and --user-id are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return mcpserver.Run(ctx, mcpserver.Config{
		DBPath: *dbPath,
		UserID: *userID,
		RunID:  *runID,
	})
}
