package csapi_tests

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/runtime"
)

func TestContent(t *testing.T) {
	// Synapse no longer allows downloads over the unauthenticated media endpoints by default
	runtime.SkipIf(t, runtime.Synapse)

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	wantContentType := "image/png"
	// sytest: POST /media/v3/upload can create an upload
	mxcUri := alice.UploadContent(t, data.MatrixPng, "test.png", wantContentType)

	// sytest: GET /media/v3/download can fetch the value again
	content, contentType := alice.DownloadContent(t, mxcUri)
	if !bytes.Equal(data.MatrixPng, content) {
		t.Fatalf("uploaded and downloaded content doesn't match: want %v\ngot\n%v", data.MatrixPng, content)
	}
	if contentType != wantContentType {
		t.Fatalf("expected contentType to be %s, got %s", wantContentType, contentType)
	}
}

// same as above but testing _matrix/client/v1/media/download
func TestContentCSAPIMediaV1(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	wantContentType := "img/png"
	mxcUri := alice.UploadContent(t, data.MatrixPng, "test.png", wantContentType)

	content, contentType := alice.DownloadContentAuthenticated(t, mxcUri)
	if !bytes.Equal(data.MatrixPng, content) {
		t.Fatalf("uploaded and downloaded content doesn't match: want %v\ngot\n%v", data.MatrixPng, content)
	}
	if contentType != wantContentType {
		t.Fatalf("expected contentType to be \n %s, got \n %s", wantContentType, contentType)
	}

	// Remove the AccessToken and try again, this should now return a 401.
	alice.AccessToken = ""
	res := alice.Do(t, "GET", []string{"_matrix", "client", "v1", "media", "download", "hs1", mxcUri})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTP status: %d, got %d", http.StatusUnauthorized, res.StatusCode)
	}
}
