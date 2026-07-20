package main

import (
	"flag"
	"image/jpeg"
	"os"
	"strconv"
	"syscall"
)

func main() {
	inputPath := flag.String("input", "", "input JPEG")
	outputPath := flag.String("output", "", "output JPEG")
	flag.Parse()
	if *inputPath == "" || *outputPath == "" {
		os.Exit(2)
	}
	applyLimit(syscall.RLIMIT_AS, "ISUREAL_SANITIZER_MEMORY_BYTES")
	applyLimit(syscall.RLIMIT_CPU, "ISUREAL_SANITIZER_CPU_SECONDS")
	input, err := os.Open(*inputPath)
	if err != nil {
		os.Exit(70)
	}
	picture, err := jpeg.Decode(input)
	input.Close()
	if err != nil {
		os.Exit(65)
	}
	output, err := os.OpenFile(*outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(70)
	}
	err = jpeg.Encode(output, picture, &jpeg.Options{Quality: 90})
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(*outputPath)
		os.Exit(70)
	}
}

func applyLimit(resource int, environment string) {
	raw := os.Getenv(environment)
	if raw == "" {
		return
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		os.Exit(2)
	}
	if err := syscall.Setrlimit(resource, &syscall.Rlimit{Cur: value, Max: value}); err != nil {
		os.Exit(70)
	}
}
