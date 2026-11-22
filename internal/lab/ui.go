package lab

import (
	"embed"
	"io/fs"
	"net/http"
)

// webFiles is the built web UI. The repository holds a placeholder; the
// container build puts the frontend from web/lab/ here before it
// compiles the binary.
//
//go:embed web
var webFiles embed.FS

func uiHandler() http.Handler {
	files, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err) // "web" is a constant, valid path
	}
	return http.FileServerFS(files)
}
