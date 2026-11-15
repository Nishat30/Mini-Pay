// admin.go routes admin subcommands: create-merchant, check-ledger.
// It owns CLI parsing for admin operations.
// It must never bypass security checks.
// It relies on internal packages for the actual work.
package main

import (
	"fmt"
	"log/slog"

	"github.com/minipay/minipay/internal/platform"
)

func runAdmin(cfg *platform.Config, logger *slog.Logger, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minipay admin <command>\n\nCommands:\n  create-merchant   Create a new merchant\n  check-ledger      Verify ledger invariants")
	}

	switch args[0] {
	case "create-merchant":
		// TODO(phase-1): implement merchant creation.
		return fmt.Errorf("create-merchant not yet implemented")
	case "check-ledger":
		// TODO(phase-1): implement ledger invariant checker.
		return fmt.Errorf("check-ledger not yet implemented")
	default:
		return fmt.Errorf("unknown admin command: %s", args[0])
	}
}
