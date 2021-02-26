package b

import "fmt"

var userCount = 500

// BlueprintOneToOneRoom contains a homeserver with 500 E2EE capable users,
// who are joined to the same room.
var BlueprintPerfE2EERoom = MustValidate(Blueprint{
	Name: "perf_e2ee_room",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
			Users: append([]User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
					E2E:         true,
					DeviceId:    Ptr("ALDJLSKJD"),
				},
				{
					Localpart:   "@bob",
					DisplayName: "Bob",
					DeviceId:    Ptr("BOBDASLDKJ"),
				},
			}, manyUsers(userCount)...),
			Rooms: []Room{
				{
					CreateRoom: map[string]interface{}{
						"preset": "public_chat",
					},
					Creator: "@alice",
					Events: append([]Event{
						{
							Type:     "m.room.member",
							StateKey: Ptr("@bob:hs1"),
							Content: map[string]interface{}{
								"membership": "join",
							},
							Sender: "@bob",
						},
						{
							Type: "m.room.message",
							Content: map[string]interface{}{
								"body":    "Hello world",
								"msgtype": "m.text",
							},
							Sender: "@bob",
						},
						{
							Type: "m.room.encryption",
							// Complement crashes because it doesn't
							// know how to URL encode an empty string, Element seems
							// to allow a state key with a space in it so use it instead
							StateKey: Ptr(" "),
							Content: map[string]interface{}{
								"algorithm": "m.megolm.v1.aes-sha2",
							},
							Sender: "@alice",
						},
					}, memberships(userCount)...),
				},
			},
		},
	},
})

func memberships(count int) []Event {
	events := make([]Event, count)

	for i := 0; i < count; i++ {
		localPart := fmt.Sprintf("@alice_%d", i)
		stateKey := fmt.Sprintf("%s:hs1", localPart)

		event := Event{
			Type:     "m.room.member",
			StateKey: Ptr(stateKey),
			Content: map[string]interface{}{
				"membership": "join",
			},
			Sender: localPart,
		}

		events[i] = event
	}

	return events
}

func manyUsers(count int) []User {
	users := make([]User, count)

	for i := 0; i < count; i++ {
		localPart := fmt.Sprintf("@alice_%d", i)
		displayName := fmt.Sprintf("Alice %d", i)
		deviceId := fmt.Sprintf("ALICEDEVICE%d", i)

		users[i] = User{
			Localpart:   localPart,
			DisplayName: displayName,
			E2E:         true,
			DeviceId:    Ptr(deviceId),
		}
	}

	return users
}
