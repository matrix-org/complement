package csapi_tests

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

func TestAsyncUpload(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // Dendrite doesn't support async uploads

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	var mxcURI, mediaID string
	t.Run("Create media", func(t *testing.T) {
		mxcURI = alice.CreateMedia(t)
		parts := strings.Split(mxcURI, "/")
		mediaID = parts[len(parts)-1]
	})

	origin, mediaID := client.SplitMxc(mxcURI)

	t.Run("Not yet uploaded", func(t *testing.T) {
		// Check that the media is not yet uploaded
		res := alice.Do(t, "GET", []string{"_matrix", "media", "v3", "download", origin, mediaID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusGatewayTimeout,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_YET_UPLOADED"),
				match.JSONKeyEqual("error", "Media has not been uploaded yet"),
			},
		})
	})

	wantContentType := "image/png"

	t.Run("Upload media", func(t *testing.T) {
		alice.UploadMediaAsync(t, origin, mediaID, data.MatrixPng, "test.png", wantContentType)
	})

	t.Run("Cannot upload to a media ID that has already been uploaded to", func(t *testing.T) {
		res := alice.Do(t, "PUT", []string{"_matrix", "media", "v3", "upload", origin, mediaID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusConflict,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_CANNOT_OVERWRITE_MEDIA"),
				match.JSONKeyEqual("error", "Media ID already has content"),
			},
		})
	})

	t.Run("Download media", func(t *testing.T) {
		content, contentType := alice.DownloadContentAuthenticated(t, mxcURI)
		if !bytes.Equal(data.MatrixPng, content) {
			t.Fatalf("uploaded and downloaded content doesn't match: want %v\ngot\n%v", data.MatrixPng, content)
		}
		if contentType != wantContentType {
			t.Fatalf("expected contentType to be %s, got %s", wantContentType, contentType)
		}
	})

	t.Run("Download media over _matrix/client/v1/media/download", func(t *testing.T) {
		content, contentType := alice.DownloadContentAuthenticated(t, mxcURI)
		if !bytes.Equal(data.MatrixPng, content) {
			t.Fatalf("uploaded and downloaded content doesn't match: want %v\ngot\n%v", data.MatrixPng, content)
		}
		if contentType != wantContentType {
			t.Fatalf("expected contentType to be %s, got %s", wantContentType, contentType)
		}
	})
}
