package csapi_tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
	"github.com/matrix-org/complement/should"
)

func TestPublicRooms(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)
	hostname := deployment.GetFullyQualifiedHomeserverName(t, "hs1")

	t.Run("parallel", func(t *testing.T) {
		// sytest: Can search public room list
		t.Run("Can search public room list", func(t *testing.T) {
			t.Parallel()
			authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

			roomID := authedClient.MustCreateRoom(t, map[string]any{
				"visibility": "public",
				"name":       "Test Name",
				"topic":      "Test Topic Wombles",
			})

			authedClient.MustDo(
				t,
				"POST",
				[]string{"_matrix", "client", "v3", "publicRooms"},
				client.WithJSONBody(t, map[string]any{
					"filter": map[string]any{
						"generic_search_term": "wombles",
					},
				}),
				client.WithRetryUntil(15*time.Second, func(res *http.Response) bool {
					results := parsePublicRoomsResponse(t, res)

					if len(results) != 1 {
						t.Logf("expected a single search result, got %d", len(results))
						return false
					}

					foundRoomID := results[0].Get("room_id").Str
					if foundRoomID != roomID {
						t.Logf("expected room_id %s in search results, got %s", roomID, foundRoomID)
						return false
					}

					return true
				}),
			)
		})

		// sytest: Name/topic keys are correct
		t.Run("Name/topic keys are correct", func(t *testing.T) {
			t.Parallel()
			authedClient := deployment.Register(t, "hs1", helpers.RegistrationOpts{})

			// Define room configurations
			roomConfigs := []struct {
				alias string
				name  string
				topic string
			}{
				{"publicroomalias_no_name", "", ""},
				{"publicroomalias_with_name", "name_1", ""},
				{"publicroomalias_with_topic", "", "topic_1"},
				{"publicroomalias_with_name_topic", "name_2", "topic_2"},
				{"publicroom_with_unicode_chars_name", "un nom français", ""},
				{"publicroom_with_unicode_chars_topic", "", "un topic à la française"},
				{"publicroom_with_unicode_chars_name_topic", "un nom français", "un topic à la française"},
			}

			for _, roomConfig := range roomConfigs {
				t.Run(fmt.Sprintf("Creating room with alias %s", roomConfig.alias), func(t *testing.T) {
					expectedCanonicalAlias := fmt.Sprintf("#%s:%s", roomConfig.alias, hostname)

					// Create the room
					roomOptions := map[string]interface{}{
						// Add the room to the public rooms list.
						"visibility":      "public",
						"room_alias_name": roomConfig.alias,
					}

					if roomConfig.name != "" {
						roomOptions["name"] = roomConfig.name
					}
					if roomConfig.topic != "" {
						roomOptions["topic"] = roomConfig.topic
					}

					roomID := authedClient.MustCreateRoom(t, roomOptions)
					t.Logf("Created room %s with alias %s", roomID, roomConfig.alias)

					// Poll /publicRooms until the room appears with the correct data

					// Keep track of any rooms that we didn't expect to see.
					unexpectedRooms := make([]string, 0)

					var discoveredRoomData gjson.Result
					authedClient.MustDo(t, "GET", []string{"_matrix", "client", "v3", "publicRooms"},
						client.WithRetryUntil(authedClient.SyncUntilTimeout, func(res *http.Response) bool {
							results := parsePublicRoomsResponse(t, res)

							// Check each room in the public rooms list
							for _, roomData := range results {
								discoveredRoomID := roomData.Get("room_id").Str
								if discoveredRoomID != roomID {
									// Not our room, skip.
									unexpectedRooms = append(unexpectedRooms, discoveredRoomID)
									continue
								}

								// We found our room. Stop calling /publicRooms and validate the response.
								discoveredRoomData = roomData
							}

							if !discoveredRoomData.Exists() {
								t.Logf("Room %s not found in public rooms list, trying again...", roomID)
								return false
							}

							return true
						}),
					)

					t.Logf("Warning: Found unexpected rooms in public rooms list: %v", unexpectedRooms)

					// Verify required keys are present in the room data.
					err := should.MatchGJSON(
						discoveredRoomData,
						match.JSONKeyPresent("world_readable"),
						match.JSONKeyPresent("guest_can_join"),
						match.JSONKeyPresent("num_joined_members"),
					)
					if err != nil {
						// The room is missing required keys, and
						// it's unlikely to get them after
						// calling the method again. Let's bail out.
						t.Fatalf("Room %s data missing required keys: %s, data: %v", roomID, err.Error(), discoveredRoomData)
					}

					// Keep track of all validation errors, rather than bailing out on the first one.
					validationErrors := make([]error, 0)

					// Verify canonical alias
					canonicalAlias := discoveredRoomData.Get("canonical_alias").Str
					if canonicalAlias != expectedCanonicalAlias {
						err = fmt.Errorf("Room %s has canonical alias '%s', expected '%s'", roomID, canonicalAlias, expectedCanonicalAlias)
						validationErrors = append(validationErrors, err)
					}

					// Verify member count
					numMembers := discoveredRoomData.Get("num_joined_members").Int()
					if numMembers != 1 {
						err = fmt.Errorf("Room %s has %d members, expected 1", roomID, numMembers)
						validationErrors = append(validationErrors, err)
					}

					// Verify name field, if there is one to verify
					name := discoveredRoomData.Get("name").Str
					if roomConfig.name != "" {
						if name != roomConfig.name {
							err = fmt.Errorf("Room %s has name '%s', expected '%s'", roomID, name, roomConfig.name)
							validationErrors = append(validationErrors, err)
						}
					} else {
						if name != "" {
							err = fmt.Errorf("Room %s has unexpected name '%s', expected no name", roomID, name)
							validationErrors = append(validationErrors, err)
						}
					}

					// Verify topic field, if there is one to verify
					topic := discoveredRoomData.Get("topic").Str
					if roomConfig.topic != "" {
						if topic != roomConfig.topic {
							err = fmt.Errorf("Room %s has topic '%s', expected '%s'", roomID, topic, roomConfig.topic)
							validationErrors = append(validationErrors, err)
						}
					} else {
						if topic != "" {
							err = fmt.Errorf("Room %s has unexpected topic '%s', expected no topic", roomID, topic)
							validationErrors = append(validationErrors, err)
						}
					}

					if len(validationErrors) > 0 {
						for _, e := range validationErrors {
							t.Errorf("Validation error for room %s: %s", roomID, e.Error())
						}

						t.Fail()
					}

					// Remove the room from the public rooms list to avoid polluting other tests.
					authedClient.MustDo(
						t,
						"PUT",
						[]string{"_matrix", "client", "v3", "directory", "list", "room", roomID},
						client.WithJSONBody(t, map[string]interface{}{
							"visibility": "private",
						}),
					)
				})
			}
		})
	})
}

func parsePublicRoomsResponse(t *testing.T, res *http.Response) []gjson.Result {
	body := must.ParseJSON(t, res.Body)

	must.MatchGJSON(
		t,
		body,
		match.JSONKeyPresent("chunk"),
		match.JSONKeyTypeEqual("chunk", gjson.JSON),
	)

	chunk := body.Get("chunk")
	if !chunk.Exists() {
		t.Fatalf("`chunk` field on public rooms response does not exist, got body: %v", body)
	}
	if !chunk.IsArray() {
		t.Fatalf("`chunk` field on public rooms response is not an array, got: %v", chunk)
	}

	return chunk.Array()
}
