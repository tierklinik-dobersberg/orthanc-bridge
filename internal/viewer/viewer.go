package viewer

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/tierklinik-dobersberg/apis/pkg/spa"
)

//go:embed assets
var viewer embed.FS

func Handler() http.Handler {
	static, _ := fs.Sub(viewer, "assets")

	return spa.ServeSPA(
		http.FS(static),
		"index.html",
	)
}
