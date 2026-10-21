// Package cover serves the embedded TLS/QUIC cover site shown to ordinary browsers.
package cover

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed index.html
var files embed.FS

// Handler serves the embedded cover site.
func Handler() http.Handler {
	sub, err := fs.Sub(files, ".")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
