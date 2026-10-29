package isureal

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

// ServePublic serves e on addr (ISUREAL_PUBLIC_LISTEN), in place of nginx.
func ServePublic(e *echo.Echo, addr string) error {
	return (&http.Server{Addr: addr, Handler: e}).ListenAndServe()
}

// staticHandler serves the front end like nginx did: a file of dir, or
// index.html for the application's routes.
func staticHandler(dir string) echo.HandlerFunc {
	files := http.Dir(dir)
	return func(c *echo.Context) error {
		path := c.Request().URL.Path
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/internal/") {
			return echo.ErrNotFound
		}
		for _, name := range []string{path, "/index.html"} {
			f, err := files.Open(name)
			if err != nil {
				continue
			}
			defer f.Close()
			if st, err := f.Stat(); err == nil && !st.IsDir() {
				http.ServeContent(c.Response(), c.Request(), st.Name(), st.ModTime(), f)
				return nil
			}
		}
		return echo.ErrNotFound
	}
}
