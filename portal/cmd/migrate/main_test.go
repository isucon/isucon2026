package main

import (
	"context"
	"testing"
)

func TestRunRejectsMissingDatabaseURL(t *testing.T) {
	t.Setenv("ISUXPORTAL_DATABASE_URL", "")
	if err := run(context.Background()); err == nil {
		t.Fatal("missing database URL must fail instead of using development defaults")
	}
}
