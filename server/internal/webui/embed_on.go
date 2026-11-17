//go:build embedweb

package webui

import (
	"embed"
	"io/fs"
)

// dist is web/dist copied to internal/webui/dist by `make embed-web`.
//
//go:embed all:dist
var embedded embed.FS

// Dist returns the embedded web app, or nil when built without embedweb.
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	return sub
}
