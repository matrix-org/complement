package tests

import (
	"io/ioutil"
	"net/url"
	"strings"
	"testing"

	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/validate"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/data"
)

// TODO: add JPEG testing

// sytest: POSTed media can be thumbnailed
func TestLocalPngThumbnail(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	fileName := "test.png"
	contentType := "image/png"

	uri := alice.UploadContent(t, data.TestPngImage, fileName, contentType)

	fetchAndValidateThumbnail(t, alice, uri)
}

// sytest: Remote media can be thumbnailed
func TestRemotePngThumbnail(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

	fileName := "test.png"
	contentType := "image/png"

	uri := alice.UploadContent(t, data.TestPngImage, fileName, contentType)

	fetchAndValidateThumbnail(t, bob, uri)
}

func fetchAndValidateThumbnail(t *testing.T, c *client.CSAPI, mxcUri string) {
	t.Helper()

	origin, mediaId := client.SplitMxc(mxcUri)

	res := c.MustDoFunc(t, "GET", []string{"_matrix", "media", "r0", "thumbnail", origin, mediaId}, client.WithQueries(url.Values{
		"width":  []string{"32"},
		"height": []string{"32"},
		"method": []string{"scale"},
	}))

	if res.StatusCode != 200 {
		t.Fatalf("thumbnail request for uploaded file failed")
	}

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		t.Fatalf("thumbnail request for uploaded file failed: %s", err)
	}

	contentType := res.Header.Get("Content-Type")
	mimeType := strings.Split(contentType, ";")[0]

	if mimeType == "image/png" {
		err = validate.ValidatePng(body)

		if err != nil {
			t.Fatalf("validating thumbnail png failed: %s", err)
		}
	} else {
		// TODO: more mimetypes
		t.Fatalf("Encountered unknown mimetype %s", mimeType)
	}

	// todo test for thumbnail size conformity?
}
