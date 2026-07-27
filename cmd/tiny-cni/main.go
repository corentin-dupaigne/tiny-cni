package main

import (
	"fmt"
	"github.com/corentin-dupaigne/tiny-cni/internal/cni"
	"github.com/corentin-dupaigne/tiny-cni/internal/config"
	"io"
	"log/slog"
	"os"
)

func main() {

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	err := run()

	if err != nil {
		slog.Error("Error while executing command", "error", err)
		os.Exit(-1)
	}

	os.Exit(0)
}

func run() error {

	args := cni.LoadArgs()

	slog.Debug("Loaded CNI args", "args", args)

	rawConfig, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading config from stdin: %w", err)
	}

	config, err := config.Parse(rawConfig)

	if err != nil {
		return err
	}

	slog.Debug("Config has been parsed", "config", config)

	switch args.Command {
	case "ADD":
		return cni.Add(args, config)
	case "DEL":
		return cni.Del(args, config)
	case "CHECK":
		return cni.Check(args, config)
	}

	return nil
}
