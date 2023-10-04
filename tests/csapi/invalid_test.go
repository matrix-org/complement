package csapi_tests

import (
	"strings"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/match"
	"github.com/matrix-org/complement/internal/must"
	"github.com/matrix-org/complement/runtime"
)

func TestJson(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"room_opts": map[string]interface{}{
			"room_version": "6",
		},
	})

	t.Run("Parallel", func(t *testing.T) {
		// sytest: Invalid JSON integers
		// sytest: Invalid JSON floats
		t.Run("Invalid numerical values", func(t *testing.T) {
			t.Parallel()

			testCases := [][]byte{
				[]byte(`{"body": 9007199254740992}`),
				[]byte(`{"body": -9007199254740992}`),
				[]byte(`{"body": 1.1}`),
			}

			for _, testCase := range testCases {
				res := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "complement.dummy"}, client.WithJSONBody(t, testCase))

				must.MatchResponse(t, res, match.HTTPResponse{
					StatusCode: 400,
					JSON: []match.JSON{
						match.JSONKeyEqual("errcode", "M_BAD_JSON"),
					},
				})
			}
		})

		// sytest: Invalid JSON special values
		t.Run("Invalid JSON special values", func(t *testing.T) {
			t.Parallel()

			testCases := [][]byte{
				[]byte(`{"body": Infinity}`),
				[]byte(`{"body": -Infinity}`),
				[]byte(`{"body": NaN}`),
			}

			for _, testCase := range testCases {
				res := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "complement.dummy"}, client.WithJSONBody(t, testCase))

				must.MatchResponse(t, res, match.HTTPResponse{
					StatusCode: 400,
				})
			}
		})
	})
}

// small helper function to not bloat the main one
// todo: this should be more exhaustive and up-to-date
// todo: this should be easier to construct
func getFilters() []map[string]interface{} {
	const NAO = "not_an_object"
	const NAL = "not_a_list"

	return []map[string]interface{}{
		{
			"presence": NAO,
		},

		{
			"room": map[string]interface{}{
				"timeline": NAO,
			},
		},
		{
			"room": map[string]interface{}{
				"state": NAO,
			},
		},
		{
			"room": map[string]interface{}{
				"ephemeral": NAO,
			},
		},
		{
			"room": map[string]interface{}{
				"account_data": NAO,
			},
		},

		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"rooms": NAL,
				},
			},
		},
		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"not_rooms": NAL,
				},
			},
		},
		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"senders": NAL,
				},
			},
		},
		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"not_senders": NAL,
				},
			},
		},
		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"types": NAL,
				},
			},
		},
		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"not_types": NAL,
				},
			},
		},

		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"types": []int{1},
				},
			},
		},
		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"rooms": []string{"not_a_room_id"},
				},
			},
		},
		{
			"room": map[string]interface{}{
				"timeline": map[string]interface{}{
					"senders": []string{"not_a_sender_id"},
				},
			},
		},
	}
}

// sytest: Check creating invalid filters returns 4xx
func TestFilter(t *testing.T) {
	runtime.SkipIf(t, runtime.Dendrite) // FIXME: https://github.com/matrix-org/dendrite/issues/2067

	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")

	filters := getFilters()

	for _, filter := range filters {
		res := alice.Do(t, "POST", []string{"_matrix", "client", "v3", "user", alice.UserID, "filter"}, client.WithJSONBody(t, filter))

		if res.StatusCode >= 500 || res.StatusCode < 400 {
			t.Errorf("Expected 4XX status code, got %d for testing filter %s", res.StatusCode, filter)
		}
	}
}

// sytest: Event size limits
func TestEvent(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)
	alice := deployment.Client(t, "hs1", "@alice:hs1")
	roomID := alice.CreateRoom(t, map[string]interface{}{
		"room_opts": map[string]interface{}{
			"room_version": "6",
		},
	})

	t.Run("Parallel", func(t *testing.T) {
		t.Run("Large Event", func(t *testing.T) {
			t.Parallel()

			event := map[string]interface{}{
				"msgtype": "m.text",
				"body":    strings.Repeat("and they dont stop coming ", 2700), // 2700 * 26 == 70200
			}

			res := alice.Do(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "send", "m.room.message", "1"}, client.WithJSONBody(t, event))

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 413,
			})
		})

		t.Run("Large State Event", func(t *testing.T) {
			t.Parallel()

			stateEvent := map[string]interface{}{
				"body": strings.Repeat("Dormammu, I've Come To Bargain.\n", 2200), // 2200 * 32 == 70400
			}

			res := alice.Do(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "marvel.universe.fate"}, client.WithJSONBody(t, stateEvent))

			must.MatchResponse(t, res, match.HTTPResponse{
				StatusCode: 413,
			})
		})
	})
}
