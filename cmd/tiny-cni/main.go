package main

import (
	"log/slog"
	"os"

	"github.com/containernetworking/cni/pkg/skel"
	cniSpecVersion "github.com/containernetworking/cni/pkg/version"
	"github.com/corentin-dupaigne/tiny-cni/internal/cni"
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

	funcs := skel.CNIFuncs{
		Add:   cni.Add,
		Del:   cni.Del,
		Check: nil,
	}

	skel.PluginMainFuncs(funcs, cniSpecVersion.All, "Tinycni Plugin")

	return nil
}
