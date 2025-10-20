package csapi_tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/match"
	"github.com/matrix-org/complement/must"
)

func TestPublicRooms(t *testing.T) {
	deployment := complement.Deploy(t, 1)
	defer deployment.Destroy(t)

	t.Run("parallel", func(t *testing.T) {
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

					// Track which rooms we've correctly found
					foundRooms := make(map[string]bool)

					// Check each room in the public rooms list
					for _, roomData := range chunk.Array() {
						// Verify required keys are present
						must.MatchGJSON(
							t,
							roomData,
							match.JSONKeyPresent("world_readable"),
							match.JSONKeyPresent("guest_can_join"),
							match.JSONKeyPresent("num_joined_members"),
						)

						canonicalAlias := roomData.Get("canonical_alias").Str
						name := roomData.Get("name").Str
						topic := roomData.Get("topic").Str
						numMembers := roomData.Get("num_joined_members").Int()

						// Skip rooms that aren't ours
						if canonicalAlias == "" {
							continue
						}

						// Find which of our rooms this matches
						var matchedAlias string
						for alias := range createdRooms {
							expectedAlias := "#" + alias + ":hs1"
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
							t.Logf("Room %s has %d members, expected 1", matchedAlias, numMembers)
							return false
						}

						// Verify name field
						if roomConfig.name != "" {
							if name != roomConfig.name {
								t.Logf("Room %s has name '%s', expected '%s'", matchedAlias, name, roomConfig.name)
								return false
							}
						} else {
							if name != "" {
								t.Logf("Room %s has unexpected name '%s', expected no name", matchedAlias, name)
								return false
							}
						}

						// Verify topic field
						if roomConfig.topic != "" {
							if topic != roomConfig.topic {
								t.Logf("Room %s has topic '%s', expected '%s'", matchedAlias, topic, roomConfig.topic)
								return false
							}
						} else {
							if topic != "" {
								t.Logf("Room %s has unexpected topic '%s', expected no topic", matchedAlias, topic)
								return false
							}
						}

						// Mark this room as correctly found
						foundRooms[matchedAlias] = true
						t.Logf("Successfully validated room %s", matchedAlias)
					}

					// Check if we found all our rooms
					if len(foundRooms) != len(createdRooms) {
						missing := []string{}
						for alias := range createdRooms {
							if !foundRooms[alias] {
								missing = append(missing, alias)
							}
						}
						t.Logf("Missing rooms in public list: %v (found %d/%d)", missing, len(foundRooms), len(createdRooms))
						return false
					}

					t.Logf("All %d rooms found with correct name/topic data", len(foundRooms))
					return true
				}),
			)
		})
	})
}
