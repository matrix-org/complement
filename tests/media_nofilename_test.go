package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/federation"
	"github.com/matrix-org/complement/must"
)

// Can handle uploads and remote/local downloads without a file name
func TestMediaWithoutFileName(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	remoteMediaId := "PlainTextFile"
	remoteFile := []byte("Hello from the other side")
	remoteContentType := "text/plain"

	srv := federation.NewServer(t, deployment,
		federation.HandleMediaRequests(map[string]func(w http.ResponseWriter){
			remoteMediaId: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", remoteContentType)
				w.WriteHeader(200)
				w.Write(remoteFile)
			},
		}),
	)
	cancel := srv.Listen()
	defer cancel()

	file := []byte("Hello World!")
	fileName := ""
	contentType := "text/plain"

	t.Run("parallel", func(t *testing.T) {
		// sytest: Can upload without a file name
		t.Run("Can upload without a file name", func(t *testing.T) {
			t.Parallel()
			mxc := alice.UploadContent(t, file, fileName, contentType)
			must.NotEqual(t, mxc, "", "did not return an MXC URI")
			must.StartWithStr(t, mxc, "mxc://", "returned invalid MXC URI")
		})
		// sytest: Can download without a file name locally
		t.Run("Can download without a file name locally", func(t *testing.T) {
			t.Parallel()
			mxc := alice.UploadContent(t, file, fileName, contentType)
			must.NotEqual(t, mxc, "", "did not return an MXC URI")
			must.StartWithStr(t, mxc, "mxc://", "returned invalid MXC URI")

			b, ct := alice.DownloadContent(t, mxc)

			// Check the Content-Type response header.
			// NOTSPEC: There is ambiguity over whether the homeserver is allowed to change the
			// Content-Type header here from what is sent by the client during upload. Synapse does
			// so, and there are benefits to doing so, but the spec needs to pick a side.
			// For now, we're operating under the assumption that homeservers are free to add other
			// directives. All we're going to check is the mime-type.
			mimeType := strings.Split(ct, ";")[0]
			must.Equal(
				t, mimeType, contentType,
				fmt.Sprintf(
					"Wrong mime-type returned in Content-Type returned. got Content-Type '%s', extracted mime-type '%s', expected mime-type: '%s'",
					ct, mimeType, contentType,
				),
			)
			must.Equal(t, string(b), string(file), "wrong file content returned")
		})
		// sytest: Can download without a file name over federation
		t.Run("Can download without a file name over federation", func(t *testing.T) {
			t.Parallel()

			b, ct := alice.DownloadContent(t, fmt.Sprintf("mxc://%s/%s", srv.ServerName(), remoteMediaId))

			// Check the Content-Type response header.
			// NOTSPEC: There is ambiguity over whether the homeserver is allowed to change the
			// Content-Type header here from what is sent by the client during upload. Synapse does
			// so, and there are benefits to doing so, but the spec needs to pick a side.
			// For now, we're operating under the assumption that homeservers are free to add other
			// directives. All we're going to check is the mime-type.
			mimeType := strings.Split(ct, ";")[0]
			must.Equal(
				t, mimeType, contentType,
				fmt.Sprintf(
					"Wrong mime-type returned in Content-Type returned. got Content-Type '%s', extracted mime-type '%s', expected mime-type: '%s'",
					ct, mimeType, contentType,
				),
			)
			must.Equal(t, string(b), string(remoteFile), "wrong file content returned")
		})
	})
}
