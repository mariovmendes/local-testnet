package l1

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/spf13/cobra"
)

var CMD = &cobra.Command{
	Use:   "l1",
	Short: "Commands for running L1 network",
	// PreRunE runs after root's PersistentPreRunE, letting us silence the
	// structured JSON logger before the L1 service writes its own terminal UI.
	PreRunE: func(cmd *cobra.Command, args []string) error {
		logger.Initialize(slog.LevelError)
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := start(cmd.Context()); err != nil {
			fmt.Fprintf(os.Stderr, "\n  \033[1;31m✗\033[0m  L1 failed: %v\n\n", err)
			return fmt.Errorf("error occurred starting l1: %w", err)
		}
		return nil
	},
}
