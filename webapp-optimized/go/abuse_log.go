package isureal

import (
	"encoding/json"
	"os"

	"github.com/labstack/echo/v5"
)

// logAbuseFailure emits only failures that the IP guard should count.
func logAbuseFailure(c *echo.Context, action string, status int, kind string) {
	_, _ = os.Stdout.Write(abuseLogLine(c.RealIP(), action, status, kind))
}

func abuseLogLine(ip string, action string, status int, kind string) []byte {
	line, _ := json.Marshal(map[string]any{
		"level":  "info",
		"action": action,
		"status": status,
		"ip":     ip,
		"abuse":  kind,
	})
	return append(line, '\n')
}
