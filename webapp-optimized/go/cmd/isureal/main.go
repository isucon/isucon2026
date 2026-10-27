package main

import (
	"context"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"

	"example.com/isureal-company/isureal"
)

// 2026-09-23

func main() {
	config := isureal.ConfigFromEnv()
	// `isureal pack-images` copies the initial data's images to
	// ISUREAL_IMAGE_DIR once.
	if len(os.Args) > 1 && os.Args[1] == "pack-images" {
		if err := isureal.BuildImagePack(context.Background(), config, os.Getenv("ISUREAL_IMAGE_DIR")); err != nil {
			slog.Error("pack-images", "error", err)
			os.Exit(1)
		}
		return
	}
	// ISUREAL_PPROF=127.0.0.1:6060 serves the Go profiler for tuning.
	if addr := os.Getenv("ISUREAL_PPROF"); addr != "" {
		go func() {
			if err := http.ListenAndServe(addr, nil); err != nil {
				slog.Error("pprof", "error", err)
			}
		}()
	}
	server := isureal.NewServer(config)
	if err := server.Start(":8080"); err != nil {
		panic("failed to start server: " + err.Error())
	}
}
