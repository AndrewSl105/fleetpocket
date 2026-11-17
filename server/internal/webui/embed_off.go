//go:build !embedweb

package webui

import "io/fs"

// Dist returns the embedded web app, or nil when built without embedweb.
func Dist() fs.FS { return nil }
