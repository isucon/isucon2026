package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TeamNumber != 1 || cfg.RouterMode != RouterModeStandIn || cfg.StandInProfile != StandInSuccess {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	tests := []map[string]string{
		{"PORTAL_TEAM_NUMBER": "0"},
		{"PORTAL_ROUTER_MODE": "other"},
		{"PORTAL_STANDIN_PROFILE": "other"},
		{"PORTAL_ROUTER_MODE": "external-router", "PORTAL_STANDIN_PROFILE": "no-report"},
		{"PORTAL_APPLY_TIMEOUT_MS": "0"},
	}
	for _, values := range tests {
		_, err := LoadFrom(func(key string) (string, bool) {
			value, ok := values[key]
			return value, ok
		})
		if err == nil {
			t.Fatalf("expected error for %#v", values)
		}
	}
}

func TestExternalRouterClearsDefaultProfile(t *testing.T) {
	cfg, err := LoadFrom(func(key string) (string, bool) {
		if key == "PORTAL_ROUTER_MODE" {
			return "external-router", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StandInProfile != "" {
		t.Fatalf("external router exposed stand-in profile %q", cfg.StandInProfile)
	}
}

func TestHostnameInputsRemainStartupValues(t *testing.T) {
	values := map[string]string{
		"PORTAL_TEAM_HASH":     "abc123",
		"PORTAL_DOMAIN_SUFFIX": "example.test",
	}
	cfg, err := LoadFrom(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.TeamHash, "abc123") || cfg.DomainSuffix != "example.test" {
		t.Fatalf("unexpected hostname config: %+v", cfg)
	}
}
