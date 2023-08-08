package tests

import (
	"fmt"
	"mime"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/data"
)

const asciiFileName = "ascii"
const unicodeFileName = "\xf0\x9f\x90\x94"

func TestMediaFilenames(t *testing.T) {
	deployment := Deploy(t, b.BlueprintFederationOneToOneRoom)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")
	bob := deployment.Client(t, "hs2", "@bob:hs2")

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
					t.Parallel()

					mxcUri := alice.UploadContent(t, data.MatrixPng, filename, "image/png")

					name, isAttachment := downloadForFilename(t, alice, mxcUri, "")

					// filename is not required, but if it's an attachment then check it matches
					if isAttachment && name != filename {
						t.Fatalf("Incorrect filename '%s', expected '%s'", name, filename)
					}
				})
			}

			// sytest: Can download specifying a different ASCII file name
			t.Run("Can download specifying a different ASCII file name", func(t *testing.T) {
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, "test.png", "image/png")

				const altName = "file.png"
				filename, isAttachment := downloadForFilename(t, alice, mxcUri, altName)

				if isAttachment && filename != altName {
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
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				const diffUnicodeFilename = "\u2615" // coffee emoji

				filename, isAttachment := downloadForFilename(t, alice, mxcUri, diffUnicodeFilename)

				if isAttachment && filename != diffUnicodeFilename {
					t.Fatalf("filename did not match, expected '%s', got '%s'", diffUnicodeFilename, filename)
				}
			})

			// sytest: Can download with Unicode file name locally
			t.Run("Can download with Unicode file name locally", func(t *testing.T) {
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				filename, isAttachment := downloadForFilename(t, alice, mxcUri, "")

				if isAttachment && filename != unicodeFileName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", unicodeFileName, filename)
				}
			})

			// sytest: Can download with Unicode file name over federation
			t.Run("Can download with Unicode file name over federation", func(t *testing.T) {
				t.Parallel()

				mxcUri := alice.UploadContent(t, data.MatrixPng, unicodeFileName, "image/png")

				filename, isAttachment := downloadForFilename(t, bob, mxcUri, "")

				if isAttachment && filename != unicodeFileName {
					t.Fatalf("filename did not match, expected '%s', got '%s'", unicodeFileName, filename)
				}
			})
		})
	})
}

// Returns content disposition information like (mediatype, filename)
func downloadForFilename(t *testing.T, c *client.CSAPI, mxcUri string, diffName string) (filename string, isAttachment bool) {
	t.Helper()

	origin, mediaId := client.SplitMxc(mxcUri)

	var path []string

	if diffName != "" {
		path = []string{"_matrix", "media", "v3", "download", origin, mediaId, diffName}
	} else {
		path = []string{"_matrix", "media", "v3", "download", origin, mediaId}
	}

	res := c.MustDoFunc(t, "GET", path)

	mediaType, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("Got err when parsing content disposition: %s", err)
	}

	if mediaType = "attachment" ||  {
		if filename, ok := params["filename"]; ok {
			return filename, true
		} else {
			t.Fatalf("Content Disposition did not have filename")
			return "", true
		}
	}
	if mediaType != "inline" {
		t.Fatalf("Found unexpected mediatype %s, expected attachment", mediaType)
	}
	return "", false
}
