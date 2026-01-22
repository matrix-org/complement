package csapi_tests

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/ct"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

const pngContentType = "image/png"

func TestAsyncUpload(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // Dendrite doesn't support async uploads

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	t.Run("Create media", func(t *testing.T) {
		alice.CreateMedia(t)
	})

	t.Run("Not yet uploaded", func(t *testing.T) {
		mxcURI := alice.CreateMedia(t)
		parts := strings.Split(mxcURI, "/")
		mediaID := parts[len(parts)-1]
		origin, mediaID := client.SplitMxc(mxcURI)

		// Check that the media is not yet uploaded
		res := alice.Do(t, "GET", []string{"_matrix", "media", "v3", "download", origin, mediaID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusGatewayTimeout,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_NOT_YET_UPLOADED"),
				match.JSONKeyPresent("error"),
			},
		})
	})

	t.Run("Upload media", func(t *testing.T) {
		mxcURI := alice.CreateMedia(t)
		parts := strings.Split(mxcURI, "/")
		mediaID := parts[len(parts)-1]
		origin, mediaID := client.SplitMxc(mxcURI)

		alice.UploadMediaAsync(t, origin, mediaID, data.MatrixPng, "test.png", pngContentType)
	})

	t.Run("Cannot upload to a media ID that has already been uploaded to", func(t *testing.T) {
		// First upload some media that we can conflict with
		mxcURI := asyncUploadMedia(t, alice)
		parts := strings.Split(mxcURI, "/")
		mediaID := parts[len(parts)-1]
		origin, mediaID := client.SplitMxc(mxcURI)

		// Then try upload again using the same `mediaID`
		res := alice.Do(t, "PUT", []string{"_matrix", "media", "v3", "upload", origin, mediaID})
		must.MatchResponse(t, res, match.HTTPResponse{
			StatusCode: http.StatusConflict,
			JSON: []match.JSON{
				match.JSONKeyEqual("errcode", "M_CANNOT_OVERWRITE_MEDIA"),
				match.JSONKeyPresent("error"),
			},
		})
	})

	// TODO: This is the same as the test below (both use authenticated media). Previously
	// this was testing unauthenticated vs authenticated media. We should resolve this by
	// removing one of these tests or ideally, keeping both authenticated and
	// unauthenticated tests and just gate it behind some homeserver check for
	// unauthenticated media support (see
	// https://github.com/matrix-org/complement/pull/746#discussion_r2718904066)
	t.Run("Download media", func(t *testing.T) {
		mxcURI := asyncUploadMedia(t, alice)

		content, contentType := alice.DownloadContentAuthenticated(t, mxcURI)
		if !bytes.Equal(data.MatrixPng, content) {
			t.Fatalf("uploaded and downloaded content doesn't match: want %v\ngot\n%v", data.MatrixPng, content)
		}
		if contentType != pngContentType {
			t.Fatalf("expected contentType to be %s, got %s", pngContentType, contentType)
		}
	})

	t.Run("Download media over _matrix/client/v1/media/download", func(t *testing.T) {
		mxcURI := asyncUploadMedia(t, alice)

		content, contentType := alice.DownloadContentAuthenticated(t, mxcURI)
		if !bytes.Equal(data.MatrixPng, content) {
			t.Fatalf("uploaded and downloaded content doesn't match: want %v\ngot\n%v", data.MatrixPng, content)
		}
		if contentType != pngContentType {
			t.Fatalf("expected contentType to be %s, got %s", pngContentType, contentType)
		}
	})
}

func asyncUploadMedia(
	t ct.TestLike,
	matrixClient *client.CSAPI,
) string {
	t.Helper()

	mxcURI := matrixClient.CreateMedia(t)
	parts := strings.Split(mxcURI, "/")
	mediaID := parts[len(parts)-1]
	origin, mediaID := client.SplitMxc(mxcURI)
	matrixClient.UploadMediaAsync(t, origin, mediaID, data.MatrixPng, "test.png", pngContentType)

	return mxcURI
}
