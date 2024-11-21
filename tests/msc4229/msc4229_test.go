package tests

import (
	"fmt"
	"github.com/matrix-org/complement"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

// Create two homeservers, one of which has a user that uploads device-keys data with
// "unsigned" data.
//
// Check that users on both servers see the unsigned data.
func TestUnsignedDeviceDataIsReturned(t *testing.T) {
	deployment := complement.Deploy(t, 2)
	defer deployment.Destroy(t)
	hs1user := deployment.Register(t, "hs1", helpers.RegistrationOpts{})
	hs2user := deployment.Register(t, "hs2", helpers.RegistrationOpts{})

	hs1userDeviceKeys, _ := hs1user.MustGenerateOneTimeKeys(t, 0)
	hs1userDeviceKeys["unsigned"] = map[string]interface{}{"a": "b"}
	hs1user.MustUploadKeys(t, hs1userDeviceKeys, nil)

	// Have each user request the keys, and check the unsigned data is there
	for _, user := range []*client.CSAPI{hs1user, hs2user} {
		res := user.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"},
			client.WithJSONBody(t, map[string]any{"device_keys": map[string]any{hs1user.UserID: []string{}}}),
		)
		must.MatchResponse(t, res, match.HTTPResponse{
			JSON: []match.JSON{
				match.JSONKeyPresent(fmt.Sprintf(
					"device_keys.%s.%s", hs1user.UserID, hs1user.DeviceID,
				)),
				match.JSONKeyEqual(fmt.Sprintf(
					"device_keys.%s.%s.unsigned", hs1user.UserID, hs1user.DeviceID,
				), map[string]interface{}{"a": "b"}),
			},
		})
	}

	// If a displayname is set, that is added tp the unsigned data, for the local user's query only
	hs1user.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "devices", hs1user.DeviceID},
		client.WithJSONBody(t, map[string]any{"display_name": "complement"}),
	)

	res := hs1user.MustDo(t, "POST", []string{"_matrix", "client", "v3", "keys", "query"},
		client.WithJSONBody(t, map[string]any{"device_keys": map[string]any{hs1user.UserID: []string{}}}),
	)
	must.MatchResponse(t, res, match.HTTPResponse{
		JSON: []match.JSON{
			match.JSONKeyPresent(fmt.Sprintf(
				"device_keys.%s.%s", hs1user.UserID, hs1user.DeviceID,
			)),
			match.JSONKeyEqual(fmt.Sprintf(
				"device_keys.%s.%s.unsigned", hs1user.UserID, hs1user.DeviceID,
			), map[string]interface{}{"a": "b"}),
		},
	})
}
