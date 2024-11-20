package tests

import (
	"bytes"
	"context"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/runtime"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
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

    t.Run("test /_matrix/media/v3 endpoint", func(t *testing.T) {
		// Synapse no longer allows downloads over the unauthenticated media endpoints by default
    	runtime.SkipIf(t, runtime.Synapse)
		fetchAndValidateThumbnail(t, alice, uri, false)
    })

	t.Run("test /_matrix/client/v1/media endpoint", func(t *testing.T) {
		runtime.SkipIf(t, runtime.Dendrite)
		fetchAndValidateThumbnail(t, alice, uri, true)
	})

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

    t.Run("test /_matrix/media/v3 endpoint", func(t *testing.T) {
		// Synapse no longer allows downloads over the unauthenticated media endpoints by default
    	runtime.SkipIf(t, runtime.Synapse)
		fetchAndValidateThumbnail(t, bob, uri, false)
    })

	t.Run("test /_matrix/client/v1/media endpoint", func(t *testing.T) {
		runtime.SkipIf(t, runtime.Dendrite)
		fetchAndValidateThumbnail(t, bob, uri, true)
	})

	// Remove the AccessToken and try again, this should now return a 401.
	alice.AccessToken = ""
	origin, mediaId := client.SplitMxc(uri)
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v1", "media", "thumbnail", origin, mediaId})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTP status: %d, got %d", http.StatusUnauthorized, res.StatusCode)
	}
}

func TestFederationThumbnail(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite)

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	srv := federation.NewServer(t, deployment,
		federation.HandleKeyRequests(),
	)
	cancel := srv.Listen()
	defer cancel()
	origin := spec.ServerName(srv.ServerName())

	fileName := "test.png"
	contentType := "image/png"

	uri := alice.UploadContent(t, data.LargePng, fileName, contentType)
	mediaOrigin, mediaId := client.SplitMxc(uri)

	path := []string{"_matrix", "client", "v1", "media", "thumbnail", mediaOrigin, mediaId}
	res := alice.MustDo(t, "GET", path, client.WithQueries(url.Values{
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

	fedReq := fclient.NewFederationRequest(
		"GET",
		origin,
		"hs1",
		"/_matrix/federation/v1/media/thumbnail/"+mediaId+"?method=scale&width=32&height=32",
	)

	resp, err := srv.DoFederationRequest(context.Background(), t, deployment, fedReq)
	if err != nil {
		t.Fatalf("federation thumbnail request for uploaded file failed: %s", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("federation thumbnail request for uploaded file failed with status code: %v", resp.StatusCode)
	}

	resContentType := resp.Header.Get("Content-Type")
	_, params, err := mime.ParseMediaType(resContentType)

	if err != nil {
		t.Fatalf("failed to parse multipart response: %s", err)
	}

	foundImage := false
	reader := multipart.NewReader(resp.Body, params["boundary"])
	for {
		p, err := reader.NextPart()
		if err == io.EOF { // End of the multipart content
			break
		}
		if err != nil {
			t.Fatalf("failed to read multipart response: %s", err)
		}

		partContentType := p.Header.Get("Content-Type")
		if partContentType == contentType {
			imageBody, err := io.ReadAll(p)
			if err != nil {
				t.Fatalf("failed to read multipart part %s: %s", partContentType, err)
			}
			if !bytes.Equal(imageBody, body) {
				t.Fatalf("body does not match uploaded file")
			}
			foundImage = true
		}
	}
	if !foundImage {
		t.Fatalf("No image was found in response.")
	}
}

func fetchAndValidateThumbnail(t *testing.T, c *client.CSAPI, mxcUri string, authenticated bool) {
	t.Helper()

	origin, mediaId := client.SplitMxc(mxcUri)

	var path []string

	if authenticated {
		path = []string{"_matrix", "client", "v1", "media", "thumbnail", origin, mediaId}
	} else {
		path = []string{"_matrix", "media", "v3", "thumbnail", origin, mediaId}
	}

	res := c.MustDo(t, "GET", path, client.WithQueries(url.Values{
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
