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
					OneTimeKeys: 50,
					DeviceID:    Ptr("ALDJLSKJD"),
				},
				{
					Localpart:   "@bob",
					DisplayName: "Bob",
					DeviceID:    Ptr("BOBDASLDKJ"),
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
							Type:     "m.room.encryption",
							StateKey: Ptr(""),
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
		deviceID := fmt.Sprintf("ALICEDEVICE%d", i)

		users[i] = User{
			Localpart:   localPart,
			DisplayName: displayName,
			OneTimeKeys: 50,
			DeviceID:    Ptr(deviceID),
		}
	}

	return users
}
