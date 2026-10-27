package isureal

import (
	gojson "github.com/goccy/go-json"
	"github.com/labstack/echo/v5"
)

// レスポンスのエンコードだけ高速な go-json を使う
type fastJSONSerializer struct {
	echo.DefaultJSONSerializer
}

func (fastJSONSerializer) Serialize(c *echo.Context, target any, indent string) error {
	enc := gojson.NewEncoder(c.Response())
	if indent != "" {
		enc.SetIndent("", indent)
	}
	return enc.Encode(target)
}
