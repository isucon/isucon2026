package dump

import "testing"

func TestQuoteText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "alice", `E'alice'`},
		{"single quote", "O'Brien", `E'O\'Brien'`},
		{"backslash", `a\b`, `E'a\\b'`},
		{"newline", "a\nb", `E'a\nb'`},
		{"tab and carriage return", "a\tb\rc", `E'a\tb\rc'`},
		{"control character", "a\x01b", `E'a\001b'`},
		{"utf8 passthrough", "éテ", `E'éテ'`},
		{"empty", "", `E''`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quoteText(tt.input); got != tt.want {
				t.Fatalf("quoteText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLiteral(t *testing.T) {
	text := column{name: "caption"}
	bytes := column{name: "data", isBytea: true}

	tests := []struct {
		name  string
		col   column
		value any
		want  string
	}{
		{"null", text, nil, "NULL"},
		{"text", text, "hello", `E'hello'`},
		{"bytea", bytes, `\x00ff10`, `decode('00ff10', 'hex')`},
		{"empty bytea", bytes, `\x`, `decode('', 'hex')`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := literal(tt.col, tt.value); got != tt.want {
				t.Fatalf("literal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuoteIdent(t *testing.T) {
	if got, want := quoteIdent("users"), `"users"`; got != want {
		t.Fatalf("quoteIdent = %q, want %q", got, want)
	}
	if got, want := quoteIdent(`we"ird`), `"we""ird"`; got != want {
		t.Fatalf("quoteIdent = %q, want %q", got, want)
	}
}

func TestTableOrderHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range tableOrder {
		if seen[name] {
			t.Fatalf("duplicate table in tableOrder: %s", name)
		}
		seen[name] = true
	}
}
