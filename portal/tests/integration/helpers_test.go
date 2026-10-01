package integration_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/token"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func checkerTokenParam(t *testing.T, p *testkit.Portal, teamID int64) string {
	t.Helper()
	tok, err := token.CreateChecker(p.Cfg.CheckerSecret, token.CheckerPayload{
		TeamID: teamID,
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
