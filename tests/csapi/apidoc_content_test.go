package csapi_tests

import (
	"bytes"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
)

func TestContent(t *testing.T) {
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
