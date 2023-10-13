package tests

import (
	"bytes"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
)

// TODO: add JPEG testing

// sytest: POSTed media can be thumbnailed
func TestLocalPngThumbnail(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	fileName := "test.png"
	contentType := "image/png"

	uri := alice.UploadContent(t, data.LargePng, fileName, contentType)

	fetchAndValidateThumbnail(t, alice, uri)
}

// sytest: Remote media can be thumbnailed
func TestRemotePngThumbnail(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	fileName := "test.png"
	contentType := "image/png"

	uri := alice.UploadContent(t, data.LargePng, fileName, contentType)

	fetchAndValidateThumbnail(t, bob, uri)
}

func fetchAndValidateThumbnail(t *testing.T, c *client.CSAPI, mxcUri string) {
	t.Helper()

	origin, mediaId := client.SplitMxc(mxcUri)

	res := c.MustDo(t, "GET", []string{"_matrix", "media", "v3", "thumbnail", origin, mediaId}, client.WithQueries(url.Values{
		"width":  []string{"32"},
		"height": []string{"32"},
		"method": []string{"scale"},
	}))

	if res.StatusCode != 200 {
		t.Fatalf("thumbnail request for uploaded file failed")
	}

	body, err := io.ReadAll(res.Body)

	if err != nil {
		t.Fatalf("thumbnail request for uploaded file failed: %s", err)
	}

	contentType := res.Header.Get("Content-Type")
	mimeType := strings.Split(contentType, ";")[0]

	// The spec says nothing about matching uploaded to thumbnailed mimetypes;
	// https://spec.matrix.org/v1.2/client-server-api/#thumbnails
	//
	// We're just picking common ones found "in the wild" and validate them on their own dime.
	if mimeType == "image/png" {
		_, err := png.Decode(bytes.NewReader(body))

		if err != nil {
			t.Fatalf("validating png thumbnail failed: %s", err)
		}
	} else if mimeType == "image/jpg" || mimeType == "image/jpeg" {
		_, err := jpeg.Decode(bytes.NewReader(body))

		if err != nil {
			t.Fatalf("validating jp(e)g thumbnail failed: %s", err)
		}
	} else {
		// TODO: more mimetypes?
		t.Fatalf("Encountered unknown mimetype %s", mimeType)
	}

	// We can't check for thumbnail size due to the spec's loose wording around returned thumbnails;
	// https://spec.matrix.org/v1.2/client-server-api/#thumbnails
}
