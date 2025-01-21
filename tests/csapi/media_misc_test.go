package csapi_tests

import (
	"net/url"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/internal/data"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/runtime"
)

// sytest: Can send image in room message
// sytest: Can fetch images in room
func TestRoomImageRoundtrip(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/1303

	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	mxcUri := alice.UploadContent(t, data.MatrixPng, "test.png", "image/png")

	roomId := alice.MustCreateRoom(t, map[string]interface{}{})

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

	messages := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomId, "messages"}, client.WithQueries(url.Values{
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
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	alice := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

	res := alice.MustDo(t, "GET", []string{"_matrix", "media", "v3", "config"})

	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyTypeEqual(client.GjsonEscape("m.upload.size"), gjson.Number),
		},
	})
}
