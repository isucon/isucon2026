package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

type RouterMode string

const (
	RouterModeStandIn        RouterMode = "stand-in"
	RouterModeExternalRouter RouterMode = "external-router"
)

type StandInProfile string

const (
	StandInSuccess             StandInProfile = "success"
	StandInFailOnceThenSuccess StandInProfile = "fail-once-then-success"
	StandInNoReport            StandInProfile = "no-report"
)

// Config contains startup-only settings. It remains immutable after Load.
type Config struct {
	TeamNumber       uint32
	TeamHash         string
	DomainSuffix     string
	RouterMode       RouterMode
	StandInProfile   StandInProfile
	ApplyTimeout     time.Duration
	StandInDelay     time.Duration
	StandInRetryWait time.Duration
	BenchmarkPending time.Duration
	BenchmarkStep    time.Duration
	ListenAddress    string
	WebDirectory     string
}

func Defaults() Config {
	return Config{
		TeamNumber:       1,
		TeamHash:         "local",
		DomainSuffix:     "isucon2026.westens.tokyo",
		RouterMode:       RouterModeStandIn,
		StandInProfile:   StandInSuccess,
		ApplyTimeout:     3 * time.Second,
		StandInDelay:     100 * time.Millisecond,
		StandInRetryWait: 150 * time.Millisecond,
		BenchmarkPending: 200 * time.Millisecond,
		BenchmarkStep:    120 * time.Millisecond,
		ListenAddress:    ":8080",
	}
}

func Load() (Config, error) { return LoadFrom(os.LookupEnv) }

func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	cfg := Defaults()
	var err error
	if value, ok := lookup("PORTAL_TEAM_NUMBER"); ok {
		n, parseErr := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		if parseErr != nil {
			return Config{}, fmt.Errorf("PORTAL_TEAM_NUMBER: %w", parseErr)
		}
		cfg.TeamNumber = uint32(n)
	}
	setString(lookup, "PORTAL_TEAM_HASH", &cfg.TeamHash)
	setString(lookup, "PORTAL_DOMAIN_SUFFIX", &cfg.DomainSuffix)
	setString(lookup, "PORTAL_LISTEN_ADDRESS", &cfg.ListenAddress)
	setString(lookup, "PORTAL_WEB_DIR", &cfg.WebDirectory)
	if value, ok := lookup("PORTAL_ROUTER_MODE"); ok {
		cfg.RouterMode = RouterMode(strings.TrimSpace(value))
	}
	if value, ok := lookup("PORTAL_STANDIN_PROFILE"); ok {
		cfg.StandInProfile = StandInProfile(strings.TrimSpace(value))
	}
	if cfg.ApplyTimeout, err = duration(lookup, "PORTAL_APPLY_TIMEOUT_MS", cfg.ApplyTimeout); err != nil {
		return Config{}, err
	}
	if cfg.StandInDelay, err = duration(lookup, "PORTAL_STANDIN_DELAY_MS", cfg.StandInDelay); err != nil {
		return Config{}, err
	}
	if cfg.StandInRetryWait, err = duration(lookup, "PORTAL_STANDIN_RETRY_MS", cfg.StandInRetryWait); err != nil {
		return Config{}, err
	}
	if cfg.BenchmarkPending, err = duration(lookup, "PORTAL_BENCHMARK_PENDING_MS", cfg.BenchmarkPending); err != nil {
		return Config{}, err
	}
	if cfg.BenchmarkStep, err = duration(lookup, "PORTAL_BENCHMARK_STEP_MS", cfg.BenchmarkStep); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.RouterMode == RouterModeExternalRouter {
		cfg.StandInProfile = ""
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if c.TeamNumber == 0 {
		problems = append(problems, "team number must be positive")
	}
	if strings.TrimSpace(c.TeamHash) == "" {
		problems = append(problems, "team hash must not be empty")
	}
	if strings.TrimSpace(c.DomainSuffix) == "" {
		problems = append(problems, "domain suffix must not be empty")
	}
	if strings.TrimSpace(c.ListenAddress) == "" {
		problems = append(problems, "listen address must not be empty")
	}
	switch c.RouterMode {
	case RouterModeStandIn:
		switch c.StandInProfile {
		case StandInSuccess, StandInFailOnceThenSuccess, StandInNoReport:
		default:
			problems = append(problems, "invalid stand-in profile")
		}
	case RouterModeExternalRouter:
		if c.StandInProfile != "" && c.StandInProfile != StandInSuccess {
			problems = append(problems, "stand-in profile is incompatible with external-router mode")
		}
	default:
		problems = append(problems, "invalid router mode")
	}
	for name, value := range map[string]time.Duration{
		"apply timeout":     c.ApplyTimeout,
		"stand-in delay":    c.StandInDelay,
		"stand-in retry":    c.StandInRetryWait,
		"benchmark pending": c.BenchmarkPending,
		"benchmark step":    c.BenchmarkStep,
	} {
		if value <= 0 {
			problems = append(problems, name+" must be positive")
		}
	}
	return errors.Join(toErrors(problems)...)
}

func setString(lookup func(string) (string, bool), key string, target *string) {
	if value, ok := lookup(key); ok {
		*target = strings.TrimSpace(value)
	}
}

func duration(lookup func(string) (string, bool), key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || ms <= 0 || ms > math.MaxInt64/int64(time.Millisecond) {
		return 0, fmt.Errorf("%s must be a positive millisecond integer", key)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func toErrors(problems []string) []error {
	result := make([]error, len(problems))
	for i, problem := range problems {
		result[i] = errors.New(problem)
	}
	return result
}
