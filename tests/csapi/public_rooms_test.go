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
					body := must.ParseJSON(t, res.Body)

					must.MatchGJSON(
						t,
						body,
						match.JSONKeyPresent("chunk"),
						match.JSONKeyTypeEqual("chunk", gjson.JSON),
					)

					chunk := body.Get("chunk")
					if !chunk.IsArray() {
						t.Logf("chunk is not an array")
						return false
					}

					results := chunk.Array()
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

			// Define room configurations matching the Sytest
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

			// Create all rooms with their configurations
			createdRooms := make(map[string]struct {
				roomID string
				name   string
				topic  string
			})

			for _, config := range roomConfigs {
				roomOptions := map[string]interface{}{
					"visibility":      "public",
					"room_alias_name": config.alias,
				}

				if config.name != "" {
					roomOptions["name"] = config.name
				}
				if config.topic != "" {
					roomOptions["topic"] = config.topic
				}

				roomID := authedClient.MustCreateRoom(t, roomOptions)
				createdRooms[config.alias] = struct {
					roomID string
					name   string
					topic  string
				}{
					roomID: roomID,
					name:   config.name,
					topic:  config.topic,
				}
				t.Logf("Created room %s with alias %s", roomID, config.alias)
			}

			// Poll /publicRooms until all our rooms appear with correct data
			authedClient.MustDo(t, "GET", []string{"_matrix", "client", "v3", "publicRooms"},
				client.WithRetryUntil(authedClient.SyncUntilTimeout, func(res *http.Response) bool {
					body := must.ParseJSON(t, res.Body)

					must.MatchGJSON(
						t,
						body,
						match.JSONKeyPresent("chunk"),
						match.JSONKeyTypeEqual("chunk", gjson.JSON),
					)

					chunk := body.Get("chunk")
					if !chunk.IsArray() {
						t.Logf("chunk is not an array")
						return false
					}

					// Track which rooms we've correctly found
					validatedRooms := make(map[string]bool)

					// Keep track of any rooms that we didn't expect to see.
					unexpectedRooms := make([]string, 0)

					// Check each room in the public rooms list
					for _, roomData := range chunk.Array() {
						roomId := roomData.Get("room_id").Str

						// Verify required keys are present. This applies to any room we see.
						err := should.MatchGJSON(
							roomData,
							match.JSONKeyPresent("world_readable"),
							match.JSONKeyPresent("guest_can_join"),
							match.JSONKeyPresent("num_joined_members"),
						)
						if err != nil {
							// This room is missing required keys, log and try again.
							t.Logf("Room %s data missing required keys: %s", roomId, err.Error())
							return false
						}

						validationErrors := make([]error, 0)

						canonicalAlias := roomData.Get("canonical_alias").Str
						name := roomData.Get("name").Str
						topic := roomData.Get("topic").Str
						numMembers := roomData.Get("num_joined_members").Int()

						// Skip rooms that aren't ours
						if canonicalAlias == "" {
							unexpectedRooms = append(unexpectedRooms, roomId)
							continue
						}

						// Find which of our rooms this matches
						var matchedAlias string
						for alias := range createdRooms {
							expectedAlias := "#" + alias + ":" + string(hostname)
							if canonicalAlias == expectedAlias {
								matchedAlias = alias
								break
							}
						}

						if matchedAlias == "" {
							continue // Not one of our rooms
						}

						roomConfig := createdRooms[matchedAlias]

						// Verify member count
						if numMembers != 1 {
							err = fmt.Errorf("Room %s has %d members, expected 1", matchedAlias, numMembers)
							validationErrors = append(validationErrors, err)
						}

						// Verify name field
						if roomConfig.name != "" {
							if name != roomConfig.name {
								err = fmt.Errorf("Room %s has name '%s', expected '%s'", matchedAlias, name, roomConfig.name)
								validationErrors = append(validationErrors, err)
							}
						} else {
							if name != "" {
								err = fmt.Errorf("Room %s has unexpected name '%s', expected no name", matchedAlias, name)
								validationErrors = append(validationErrors, err)
							}
						}

						// Verify topic field
						if roomConfig.topic != "" {
							if topic != roomConfig.topic {
								err = fmt.Errorf("Room %s has topic '%s', expected '%s'", matchedAlias, topic, roomConfig.topic)
								validationErrors = append(validationErrors, err)
							}
						} else {
							if topic != "" {
								err = fmt.Errorf("Room %s has unexpected topic '%s', expected no topic", matchedAlias, topic)
								validationErrors = append(validationErrors, err)
							}
						}

						if len(validationErrors) > 0 {
							for _, e := range validationErrors {
								t.Logf("Validation error for room %s: %s", matchedAlias, e.Error())
							}

							return false
						}

						// Mark this room as correctly found
						validatedRooms[matchedAlias] = true

						t.Logf("Successfully validated room %s", matchedAlias)
					}

					// Check if we found and validated all of our rooms
					if len(validatedRooms) != len(createdRooms) {
						missing := []string{}
						for alias := range createdRooms {
							if !validatedRooms[alias] {
								missing = append(missing, alias)
							}
						}
						t.Logf("Missing rooms in public list: %v (found %d/%d)", missing, len(validatedRooms), len(createdRooms))

						if len(unexpectedRooms) > 0 {
							t.Logf("Also found unexpected rooms: %v", unexpectedRooms)
						}
						return false
					}

					t.Logf("All %d rooms found with correct name/topic data", len(validatedRooms))
					return true
				}),
			)
		})
	})
}
