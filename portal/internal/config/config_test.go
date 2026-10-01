package config

import "testing"

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
