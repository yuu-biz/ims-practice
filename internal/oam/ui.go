package oam

import (
	"embed"
	"io/fs"
	"net/http"
)

// webFiles is the built OAM screen. The repository holds a placeholder;
// the container build puts the frontend from web/oam/ here before it
// compiles the binary.
//
//go:embed web
var webFiles embed.FS

// UIHandler serves the OAM screen. A role serves it on the same
// listener as Handler, under a path of its own: the screen calls the
// API at the absolute path /oam/v1/..., and reads the files of the page
// at paths relative to it, so it works wherever it is served.
func UIHandler() http.Handler {
	files, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err) // "web" is a constant, valid path
	}
	return http.FileServerFS(files)
}
