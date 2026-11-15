// main.go is the entry point for the minipay binary.
// It owns subcommand routing: api, worker, migrate, admin.
// It must never contain business logic.
// It relies on internal packages for all functionality.
package main

import (
	"fmt"
	"os"

	"github.com/minipay/minipay/internal/platform"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := platform.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logger := platform.NewLogger(cfg.LogLevel)

	switch os.Args[1] {
	case "api":
		if err := runAPI(cfg, logger); err != nil {
			logger.Error("api server failed", "error", err)
			os.Exit(1)
		}
	case "worker":
		if err := runWorker(cfg, logger); err != nil {
			logger.Error("worker failed", "error", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(cfg, logger); err != nil {
			logger.Error("migration failed", "error", err)
			os.Exit(1)
		}
	case "admin":
		if err := runAdmin(cfg, logger, os.Args[2:]); err != nil {
			logger.Error("admin command failed", "error", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("minipay %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: minipay <command>

Commands:
  api       Start the API server
  worker    Start the background worker
  migrate   Run database migrations
  admin     Admin commands (create-merchant, check-ledger)
  version   Print the version`)
}
