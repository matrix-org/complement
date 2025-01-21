package tests

import (
	"bytes"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
	"testing"
)

func TestContentMediaV1(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	hs1 := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	hs2 := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	wantContentType := "img/png"
	mxcUri := hs1.UploadContent(t, data.MatrixPng, "test.png", wantContentType)

	content, contentType := hs2.DownloadContentAuthenticated(t, mxcUri)
	if !bytes.Equal(data.MatrixPng, content) {
		t.Fatalf("uploaded and downloaded content doesn't match: want %v\ngot\n%v", data.MatrixPng, content)
	}
	if contentType != wantContentType {
		t.Fatalf("expected contentType to be \n %s, got \n %s", wantContentType, contentType)
	}
}
