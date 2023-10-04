package csapi_tests

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/internal/web"
	"github.com/matrix-org/complement/runtime"
)

const oGraphTitle = "The Rock"
const oGraphType = "video.movie"
const oGraphUrl = "http://www.imdb.com/title/tt0117500/"
const oGraphImage = "test.png"

var oGraphHtml = fmt.Sprintf(`
<html prefix="og: http://ogp.me/ns#">
<head>
<title>The Rock (1996)</title>
<meta property="og:title" content="%s" />
<meta property="og:type" content="%s" />
<meta property="og:url" content="%s" />
<meta property="og:image" content="%s" />
</head>
<body></body>
</html>
`, oGraphTitle, oGraphType, oGraphUrl, oGraphImage)

// sytest: Test URL preview
func TestUrlPreview(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/621

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	webServer := web.NewServer(t, complementBuilder.Config, func(router *mux.Router) {
		router.HandleFunc("/test.png", func(w http.ResponseWriter, req *http.Request) {
			t.Log("/test.png fetched")

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(200)
			w.Write(data.MatrixPng)
		}).Methods("GET")
		router.HandleFunc("/test.html", func(w http.ResponseWriter, req *http.Request) {
			t.Log("/test.html fetched")

			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(200)
			w.Write([]byte(oGraphHtml))
		}).Methods("GET")
	})
	defer webServer.Close()

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	res := alice.MustDo(t, "GET", []string{"_matrix", "media", "v3", "preview_url"},
		client.WithQueries(url.Values{
			"url": []string{webServer.URL + "/test.html"},
		}),
	)

	var e = client.GjsonEscape

	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyEqual(e("og:title"), oGraphTitle),
			match.JSONKeyEqual(e("og:type"), oGraphType),
			match.JSONKeyEqual(e("og:url"), oGraphUrl),
			match.JSONKeyEqual(e("matrix:image:size"), 2239.0),
			match.JSONKeyEqual(e("og:image:height"), 129.0),
			match.JSONKeyEqual(e("og:image:width"), 279.0),
			func(body []byte) error {
				res := gjson.GetBytes(body, e("og:image"))
				if !res.Exists() {
					return fmt.Errorf("can not find key og:image")
				}
				if !strings.HasPrefix(res.Str, "mxc://") {
					return fmt.Errorf("image is not mxc")
				}

				return nil
			},
		},
	})
}
