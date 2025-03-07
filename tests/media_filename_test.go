package tests

import (
	"fmt"
	"mime"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/runtime"
)

const asciiFileName = "ascii"
const unicodeFileName = "\xf0\x9f\x90\x94"

func TestMediaFilenames(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	bob := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	t.Run("Parallel", func(t *testing.T) {

		t.Run("ASCII", func(t *testing.T) {
			t.Parallel()

			// sytest: Can upload with ASCII file name
			t.Run("Can upload with ASCII file name", func(t *testing.T) {
				t.Parallel()

				alice.UploadContent(t, data.MatrixPng, asciiFileName, "image/png")
			})

			// sytest: Can download file '$filename'
			for _, filename := range []string{"ascii", "name with spaces", "name;with;semicolons"} {

				// To preserve variable in loop
				var filename = filename

				t.Run(fmt.Sprintf("Can download file '%s'", filename), func(t *testing.T) {
					// Synapse no longer allows downloads over the unauthenticated media endpoints by default
					runtime.SkipIf(t, runtime.Synapse)
					t.Parallel()

					mxcUri := alice.UploadContent(t, data.MatrixPng, filename, "image/png")

					name, _ := downloadForFilename(t, alice, mxcUri, "", false)

					// filename is not required, but if it's an attachment then check it matches
					if name != filename {
						t.Fatalf("Incorrect filename '%s', expected '%s'", name, filename)
					}
				})

				t.Run(fmt.Sprintf("Can download file '%s' over /_matrix/client/v1/media/download", filename), func(t *testing.T) {
					t.Parallel()

					mxcUri := alice.UploadContent(t, data.MatrixPng, filename, "image/png")

					name, _ := downloadForFilename(t, alice, mxcUri, "", true)

					// filename is not required, but if it's an attachment then check it matches
					if name != filename {
						t.Fatalf("Incorrect filename '%s', expected '%s'", name, filename)
					}
				})
			}

			// sytest: Can download specifying a different ASCII file name
			t.Run("Can download specifying a different ASCII file name", func(t *testing.T) {
				// Synapse no longer allows downloads over the unauthenticated media endpoints by default
				runtime.SkipIf(t, runtime.Synapse)
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, asciiFileName, "image/png")

				const altName = "file.png"
				filename, _ := downloadForFilename(t, alice, mxcUri, altName, false)

				if filename != altName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", altName, filename)
				}
			})
			t.Run("Can download specifying a different ASCII file name over _matrix/client/v1/media/download", func(t *testing.T) {
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, asciiFileName, "image/png")

				const altName = "file.png"
				filename, _ := downloadForFilename(t, alice, mxcUri, altName, true)

				if filename != altName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", altName, filename)
				}
			})

		})

		t.Run("Unicode", func(t *testing.T) {
			t.Parallel()

			// sytest: Can upload with Unicode file name
			t.Run("Can upload with Unicode file name", func(t *testing.T) {
				t.Parallel()

				alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")
			})

			// sytest: Can download specifying a different Unicode file name
			t.Run("Can download specifying a different Unicode file name", func(t *testing.T) {
				// Synapse no longer allows downloads over the unauthenticated media endpoints by default
				runtime.SkipIf(t, runtime.Synapse)
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				const diffUnicodeFilename = "\u2615" // coffee emoji

				filename, _ := downloadForFilename(t, alice, mxcUri, diffUnicodeFilename, false)

				if filename != diffUnicodeFilename {
					t.Fatalf("filename did not match, expected '%s', got '%s'", diffUnicodeFilename, filename)
				}
			})

			t.Run("Can download specifying a different Unicode file name over _matrix/client/v1/media/download", func(t *testing.T) {
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				const diffUnicodeFilename = "\u2615" // coffee emoji

				filename, _ := downloadForFilename(t, alice, mxcUri, diffUnicodeFilename, true)

				if filename != diffUnicodeFilename {
					t.Fatalf("filename did not match, expected '%s', got '%s'", diffUnicodeFilename, filename)
				}
			})

			// sytest: Can download with Unicode file name locally
			t.Run("Can download with Unicode file name locally", func(t *testing.T) {
				// Synapse no longer allows downloads over the unauthenticated media endpoints by default
				runtime.SkipIf(t, runtime.Synapse)
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				filename, _ := downloadForFilename(t, alice, mxcUri, "", false)

				if filename != unicodeFileName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", unicodeFileName, filename)
				}
			})

			t.Run("Can download with Unicode file name locally over _matrix/client/v1/media/download", func(t *testing.T) {
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				filename, _ := downloadForFilename(t, alice, mxcUri, "", true)

				if filename != unicodeFileName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", unicodeFileName, filename)
				}
			})

			// sytest: Can download with Unicode file name over federation
			t.Run("Can download with Unicode file name over federation", func(t *testing.T) {
				// Synapse no longer allows downloads over the unauthenticated media endpoints by default
				runtime.SkipIf(t, runtime.Synapse)
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				filename, _ := downloadForFilename(t, bob, mxcUri, "", false)

				if filename != unicodeFileName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", unicodeFileName, filename)
				}
			})

			t.Run("Can download with Unicode file name over federation via _matrix/client/v1/media/download", func(t *testing.T) {
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				filename, _ := downloadForFilename(t, bob, mxcUri, "", true)

				if filename != unicodeFileName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", unicodeFileName, filename)
				}
			})

			t.Run("Will serve safe media types as inline", func(t *testing.T) {
				if runtime.Homeserver != runtime.Conduwuit {
					// We need to check that this security behaviour is being correctly run in
					// conduwuit, but since this is not part of the Matrix spec we do not assume
					// other homeservers are doing so.
					// Skip Synapse because it no longer allows downloads over the unauthenticated media endpoints by default
					t.Skip("Skipping test of Content-Disposition header requirements on non-conduwuit homeserver")
				}
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, "", "image/png")

				_, isAttachment := downloadForFilename(t, bob, mxcUri, "", false)

				if isAttachment {
					t.Fatal("Expected file to be served as inline")
				}
			})

			t.Run("Will serve safe media types as inline via _matrix/client/v1/media/download", func(t *testing.T) {
				if runtime.Homeserver != runtime.Synapse && runtime.Homeserver != runtime.Conduwuit {
					// We need to check that this security behaviour is being correctly run in
					// Synapse or conduwuit, but since this is not part of the Matrix spec we do not assume
					// other homeservers are doing so.
					t.Skip("Skipping test of Content-Disposition header requirements on non-Synapse and non-conduwuit homeserver")
				}
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, "", "image/png")

				_, isAttachment := downloadForFilename(t, bob, mxcUri, "", true)

				if isAttachment {
					t.Fatal("Expected file to be served as inline")
				}
			})

			t.Run("Will serve safe media types with parameters as inline", func(t *testing.T) {
				if runtime.Homeserver != runtime.Conduwuit {
					// We need to check that this security behaviour is being correctly run in
					// conduwuit, but since this is not part of the Matrix spec we do not assume
					// other homeservers are doing so.
					// Skip Synapse because it no longer allows downloads over the unauthenticated media endpoints by default
					t.Skip("Skipping test of Content-Disposition header requirements on non-conduwuit homeserver")
				}
				t.Parallel()

				// Add parameters and upper-case, which should be parsed as text/plain.
				mxcUri := alice.UploadContent(t, data.MatrixPng, "", "Text/Plain; charset=utf-8")

				_, isAttachment := downloadForFilename(t, bob, mxcUri, "", false)

				if isAttachment {
					t.Fatal("Expected file to be served as inline")
				}
			})

			t.Run("Will serve safe media types with parameters as inline via _matrix/client/v1/media/download", func(t *testing.T) {
				if runtime.Homeserver != runtime.Synapse && runtime.Homeserver != runtime.Conduwuit {
					// We need to check that this security behaviour is being correctly run in
					// Synapse or conduwuit, but since this is not part of the Matrix spec we do not assume
					// other homeservers are doing so.
					t.Skip("Skipping test of Content-Disposition header requirements on non-Synapse and non-conduwuit homeserver")
				}
				t.Parallel()

				// Add parameters and upper-case, which should be parsed as text/plain.
				mxcUri := alice.UploadContent(t, data.MatrixPng, "", "Text/Plain; charset=utf-8")

				_, isAttachment := downloadForFilename(t, bob, mxcUri, "", true)

				if isAttachment {
					t.Fatal("Expected file to be served as inline")
				}
			})

			t.Run("Will serve unsafe media types as attachments", func(t *testing.T) {
				if runtime.Homeserver != runtime.Conduwuit {
					// We need to check that this security behaviour is being correctly run in
					// conduwuit, but since this is not part of the Matrix spec we do not assume
					// other homeservers are doing so.
					// Skip Synapse because it no longer allows downloads over the unauthenticated media endpoints by default
					t.Skip("Skipping test of Content-Disposition header requirements on non-conduwuit homeserver")
				}
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixSvg, "", "image/svg")

				_, isAttachment := downloadForFilename(t, bob, mxcUri, "", false)

				if !isAttachment {
					t.Fatal("Expected file to be served as an attachment")
				}
			})

			t.Run("Will serve unsafe media types as attachments via _matrix/client/v1/media/download", func(t *testing.T) {
				if runtime.Homeserver != runtime.Synapse && runtime.Homeserver != runtime.Conduwuit {
					// We need to check that this security behaviour is being correctly run in
					// Synapse or conduwuit, but since this is not part of the Matrix spec we do not assume
					// other homeservers are doing so.
					t.Skip("Skipping test of Content-Disposition header requirements on non-Synapse and non-conduwuit homeserver")
				}
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixSvg, "", "image/svg")

				_, isAttachment := downloadForFilename(t, bob, mxcUri, "", true)

				if !isAttachment {
					t.Fatal("Expected file to be served as an attachment")
				}
			})
		})
	})
}

// Returns content disposition information like (filename, isAttachment)
func downloadForFilename(t *testing.T, c *client.CSAPI, mxcUri string, diffName string, authenticatedEndpoint bool) (filename string, isAttachment bool) {
	t.Helper()

	origin, mediaId := client.SplitMxc(mxcUri)

	var path []string

	if diffName != "" {
		path = []string{"_matrix", "media", "v3", "download", origin, mediaId, diffName}

		if authenticatedEndpoint {
			path = []string{"_matrix", "client", "v1", "media", "download", origin, mediaId, diffName}
		}
	} else {
		path = []string{"_matrix", "media", "v3", "download", origin, mediaId}

		if authenticatedEndpoint {
			path = []string{"_matrix", "client", "v1", "media", "download", origin, mediaId}
		}
	}

	res := c.MustDo(t, "GET", path)

	mediaType, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("Got err when parsing content disposition: %s", err)
	}
	filename, hasFilename := params["filename"]
	if mediaType == "attachment" {
		if hasFilename {
			return filename, true
		} else {
			return "", true
		}
	}
	if mediaType != "inline" {
		t.Fatalf("Found unexpected mediatype %s, expected 'attachment' or 'inline'", mediaType)
	}
	return filename, false
}
