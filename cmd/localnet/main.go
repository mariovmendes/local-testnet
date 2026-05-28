package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l1"
	"github.com/ethera-labs/local-testnet/internal/l2"
	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/ethera-labs/local-testnet/internal/observability"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const appName = "local-testnet"

var rootCmd = &cobra.Command{
	Use:   appName,
	Short: "CLI for managing local L1 and L2 network",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		logger.Initialize(slog.LevelInfo)

		viper.SetConfigName("config")
		viper.SetConfigType("yaml")

		if execPath, err := os.Executable(); err == nil {
			execDir := filepath.Dir(execPath)
			viper.AddConfigPath(execDir)
		}
		viper.AddConfigPath(".")
		viper.AddConfigPath("./configs")

		// Try to read config file, but don't fail if it doesn't exist
		// Flags can provide all necessary configuration
		if err := viper.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				slog.Debug("no config file found, will rely on flags and defaults")
			} else {
				const errMsg = "error reading config file"
				slog.With("err", err.Error()).Error(errMsg)
				return errors.Join(err, errors.New(errMsg))
			}
		} else {
			slog.With("config_file", viper.ConfigFileUsed()).Debug("config file loaded")
		}

		if err := viper.Unmarshal(&configs.Values); err != nil {
			const errMsg = "unable to decode application config"
			slog.With("err", err.Error()).Error(errMsg)
			return errors.Join(err, errors.New(errMsg))
		}

		slog.With("config", configs.Values).Debug("configuration loaded")

		return nil
	},
}

func main() {
	rootCmd.Short = appName

	rootCmd.AddCommand(l1.CMD)
	rootCmd.AddCommand(l2.CMD)
	rootCmd.AddCommand(observability.CMD)

	if err := rootCmd.Execute(); err != nil {
		slog.With("err", err.Error()).Error("failed to execute root command")
		panic(err.Error())
	}
}
