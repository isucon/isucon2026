package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains all process configuration shared by the API and workers.
type Config struct {
	ListenAddress string
	PublicOrigin  string
	DatabaseURL   string
	ImageRoot     string
	CursorHMACKey string
	VAPID         VAPIDConfig
	Worker        WorkerConfig
	Sanitizer     SanitizerConfig
}

type VAPIDConfig struct {
	PublicKey  string
	PrivateKey string
	Subject    string
}

type WorkerConfig struct {
	PollInterval time.Duration
	BatchSize    int
}

type SanitizerConfig struct {
	Path        string
	Concurrency int
	MemoryBytes uint64
	CPUSeconds  uint64
	Timeout     time.Duration
}

// Load reads environment variables and rejects unsafe or internally
// inconsistent values before a process starts serving requests.
func Load() (Config, error) {
	cfg := Config{
		ListenAddress: env("ISUREAL_LISTEN_ADDRESS", ":8080"),
		PublicOrigin:  env("ISUREAL_PUBLIC_ORIGIN", "http://localhost:5173"),
		DatabaseURL:   os.Getenv("ISUREAL_DATABASE_URL"),
		ImageRoot:     env("ISUREAL_IMAGE_ROOT", "/var/lib/isureal/images"),
		CursorHMACKey: os.Getenv("ISUREAL_CURSOR_HMAC_KEY"),
		VAPID: VAPIDConfig{
			PublicKey:  os.Getenv("ISUREAL_VAPID_PUBLIC_KEY"),
			PrivateKey: os.Getenv("ISUREAL_VAPID_PRIVATE_KEY"),
			Subject:    os.Getenv("ISUREAL_VAPID_SUBJECT"),
		},
		Worker: WorkerConfig{
			PollInterval: duration("ISUREAL_WORKER_POLL_INTERVAL", time.Second),
			BatchSize:    integer("ISUREAL_WORKER_BATCH_SIZE", 50),
		},
		Sanitizer: SanitizerConfig{
			Path:        env("ISUREAL_IMAGE_SANITIZER_PATH", "/usr/local/bin/image-sanitizer"),
			Concurrency: integer("ISUREAL_IMAGE_SANITIZER_CONCURRENCY", 2),
			MemoryBytes: unsigned("ISUREAL_IMAGE_SANITIZER_MEMORY_BYTES", 512<<20),
			CPUSeconds:  unsigned("ISUREAL_IMAGE_SANITIZER_CPU_SECONDS", 10),
			Timeout:     duration("ISUREAL_IMAGE_SANITIZER_TIMEOUT", 15*time.Second),
		},
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var problems []error
	if strings.TrimSpace(c.DatabaseURL) == "" {
		problems = append(problems, errors.New("ISUREAL_DATABASE_URL is required"))
	} else if parsed, err := url.Parse(c.DatabaseURL); err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		problems = append(problems, errors.New("ISUREAL_DATABASE_URL must be a PostgreSQL URL"))
	}
	if origin, err := url.Parse(c.PublicOrigin); err != nil || origin.Scheme == "" || origin.Host == "" || origin.Path != "" {
		problems = append(problems, errors.New("ISUREAL_PUBLIC_ORIGIN must be an origin without a path"))
	}
	if c.ImageRoot == "" || !strings.HasPrefix(c.ImageRoot, "/") {
		problems = append(problems, errors.New("ISUREAL_IMAGE_ROOT must be an absolute path"))
	}
	if len(c.CursorHMACKey) < 32 {
		problems = append(problems, errors.New("ISUREAL_CURSOR_HMAC_KEY must contain at least 32 bytes"))
	}
	if c.Worker.PollInterval <= 0 || c.Worker.BatchSize < 1 || c.Worker.BatchSize > 1000 {
		problems = append(problems, errors.New("worker interval and batch size are out of range"))
	}
	if c.Sanitizer.Path == "" || c.Sanitizer.Concurrency < 1 || c.Sanitizer.MemoryBytes == 0 || c.Sanitizer.CPUSeconds == 0 || c.Sanitizer.Timeout <= 0 {
		problems = append(problems, errors.New("image sanitizer budgets must be positive"))
	}
	setVAPID := c.VAPID.PublicKey != "" || c.VAPID.PrivateKey != "" || c.VAPID.Subject != ""
	if setVAPID && (c.VAPID.PublicKey == "" || c.VAPID.PrivateKey == "" || c.VAPID.Subject == "") {
		problems = append(problems, errors.New("VAPID public key, private key, and subject must be configured together"))
	}
	return errors.Join(problems...)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return -1
	}
	return value
}

func integer(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	return value
}

func unsigned(key string, fallback uint64) uint64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func (c Config) String() string {
	return fmt.Sprintf("listen=%s origin=%s imageRoot=%s workerBatch=%d", c.ListenAddress, c.PublicOrigin, c.ImageRoot, c.Worker.BatchSize)
}
