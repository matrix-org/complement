package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/federation"
	"github.com/matrix-org/complement/internal/must"
)

// Can handle uploads and remote/local downloads without a file name
func TestMediaWithoutFileName(t *testing.T) {
	deployment := Deploy(t, "media_repo", b.BlueprintAlice)
	defer deployment.Destroy(t)

	srv := federation.NewServer(t, deployment,
		federation.HandleMediaRequests(),
	)
	cancel := srv.Listen()
	defer cancel()

	userID := "@alice:hs1"
	file := []byte("Hello World!")
	fileName := ""
	contentType := "text/plain"

	t.Run("parallel", func(t *testing.T) {
		t.Run("Can upload without a file name", func(t *testing.T) {
			t.Parallel()
			alice := deployment.Client(t, "hs1", userID)
			mxc := alice.UploadContent(t, file, fileName, contentType)
			must.NotEqualStr(t, mxc, "", "did not return an MXC URI")
			must.StartWithStr(t, mxc, "mxc://", "returned invalid MXC URI")
		})
		t.Run("Can download without a file name locally", func(t *testing.T) {
			t.Parallel()
			alice := deployment.Client(t, "hs1", userID)
			mxc := alice.UploadContent(t, file, fileName, contentType)
			must.NotEqualStr(t, mxc, "", "did not return an MXC URI")
			must.StartWithStr(t, mxc, "mxc://", "returned invalid MXC URI")
			b, ct := alice.DownloadContent(t, mxc)
			must.EqualStr(t, ct, contentType, "wrong Content-Type returned")
			must.EqualStr(t, string(b), string(file), "wrong file content returned")
		})
		t.Run("Can download without a file name over federation", func(t *testing.T) {
			t.Parallel()
			alice := deployment.Client(t, "hs1", userID)

			// We know to expect a text/plain response like this from the HandleMediaRequests docs
			wantContentType := "text/plain"
			wantResponse := "Hello from the other side"

			b, ct := alice.DownloadContent(t, fmt.Sprintf("mxc://%s/%s", srv.ServerName, "PlainTextFile"))
			must.EqualStr(t, ct, wantContentType, "wrong Content-Type returned")
			must.EqualStr(t, string(b), wantResponse, "wrong file content returned")
		})
	})
}
