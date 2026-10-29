package config

import (
	"strings"
	"testing"
)

func TestDiscordCallbackPathValidation(t *testing.T) {
	for _, path := range []string{"/auth/discord/complete", "https://example.com/callback", "/auth/github/callback"} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("ISUXPORTAL_DISCORD_CALLBACK_PATH", path)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "ISUXPORTAL_DISCORD_CALLBACK_PATH") {
				t.Fatalf("invalid callback path %q: error=%v", path, err)
			}
		})
	}
}

func TestContestLinks(t *testing.T) {
	for _, tt := range []struct {
		name, terms, rules string
	}{
		{"defaults", "", ""},
		{"overrides", "https://example.com/terms", "https://example.com/rules"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ISUXPORTAL_TERMS_URL", tt.terms)
			t.Setenv("ISUXPORTAL_RULES_URL", tt.rules)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			wantTerms, wantRules := tt.terms, tt.rules
			if tt.name == "defaults" {
				wantTerms = "https://isucon.net/archives/59967038.html"
				wantRules = "https://isucon.net/archives/59966826.html"
			}
			if cfg.TermsURL != wantTerms {
				t.Errorf("TermsURL = %q, want %q", cfg.TermsURL, wantTerms)
			}
			if cfg.RulesURL != wantRules {
				t.Errorf("RulesURL = %q, want %q", cfg.RulesURL, wantRules)
			}
		})
	}
}

func TestGitHubCallbackPathValidation(t *testing.T) {
	for _, path := range []string{"/complete/github", "https://example.com/callback", "/auth/discord/callback"} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("ISUXPORTAL_GITHUB_CALLBACK_PATH", path)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "ISUXPORTAL_GITHUB_CALLBACK_PATH") {
				t.Fatalf("invalid callback path %q: error=%v", path, err)
			}
		})
	}
}

func TestBenchLargeScoreThreshold(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    int64
		invalid bool
	}{
		{"", 0, false}, {"0", 0, false}, {"10000", 10000, false}, {"-1", 0, true}, {"oops", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("ISUXPORTAL_BENCH_LARGE_SCORE_THRESHOLD", tc.value)
			cfg, err := Load()
			if tc.invalid {
				if err == nil {
					t.Fatal("expected invalid threshold error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.BenchLargeScoreThreshold != tc.want {
				t.Fatalf("threshold %d", cfg.BenchLargeScoreThreshold)
			}
		})
	}
}
