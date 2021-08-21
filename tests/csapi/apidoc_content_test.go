package csapi_tests

import (
	"github.com/tidwall/gjson"
	"io/ioutil"
	"net/url"
	"strings"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
)

var contentID *url.URL
var contentText = "Here is the content I am uploading"

func TestContent(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	authedClient := deployment.Client(t, "hs1", "@alice:hs1")
	unauthedClient := deployment.Client(t, "hs1", "")
	// sytest: POST /media/r0/upload can create an upload
	t.Run("POST /media/r0/upload can create an upload", func(t *testing.T) {

		gotUrl := ""
		queryParams := url.Values{}
		queryParams.Set("access_token", authedClient.AccessToken)

		contentType := client.WithContentType("text/plain")
		reqBody := client.WithRawBody([]byte(contentText))
		res := unauthedClient.MustDoFunc(t, "POST", []string{"_matrix", "media", "r0", "upload"}, reqBody, contentType, client.WithQueries(queryParams))

		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("unable to read response body: %v", err)
		}

		gotUrl = gjson.GetBytes(body, "content_uri").Str
		if gotUrl == "" {
			t.Fatal("expected URL to hae propogated the value")
		}

		contentID, err = url.Parse(gotUrl)
		if err != nil {
			t.Fatal("invalid URL returned in response")
		}
	})
	// sytest: GET /media/r0/download can fetch the value again
	t.Run("GET /media/r0/download can fetch the value again", func(t *testing.T) {

		res := unauthedClient.MustDoFunc(t, "GET", []string{"_matrix", "media", "r0", "download", contentID.Host, strings.Trim(contentID.Path, "/")})

		responseData, err := ioutil.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("unable to read response body: %v", err)
		}

		if string(responseData) != contentText {
			t.Fatal("failed to fetch valid content text")
		}
	})
}
