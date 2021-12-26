package csapi_tests

import (
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

// sytest: Can send image in room message
// sytest: Can fetch images in room
func TestRoomImageRoundtrip(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1303

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	mxcUri := alice.UploadContent(t, data.TestPngImage, "test.png", "image/png")

	roomId := alice.CreateRoom(t, struct{}{})

	alice.SendEventSynced(t, roomId, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "test",
		},
	})

	alice.SendEventSynced(t, roomId, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.file",
			"body":    "test.png",
			"url":     mxcUri,
		},
	})

	messages := alice.MustDoFunc(t, "GET", []string{"_matrix", "client", "r0", "rooms", roomId, "messages"}, client.WithQueries(url.Values{
		"filter": []string{`{"contains_url":true}`},
		"dir":    []string{"b"},
	}))

	must.MatchResponse(t, messages, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyTypeEqual("start", gjson.String),
			match.JSONKeyTypeEqual("end", gjson.String),
			match.JSONKeyArrayOfSize("chunk", 1),
		},
	})
}

// sytest: Can read configuration endpoint
func TestMediaConfig(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	res := alice.MustDoFunc(t, "GET", []string{"_matrix", "media", "r0", "config"})

	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyTypeEqual(client.GjsonEscape("m.upload.size"), gjson.Number),
		},
	})
}
