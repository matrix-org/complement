package b

// BlueprintFederationOneToOneRoom contains two homeservers with 1 user in each, who are joined
// to the same room.
var BlueprintFederationOneToOneRoom = MustValidate(Blueprint{
	Name: "federation_one_to_one_room",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
			Users: []User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
				},
			},
			Rooms: []Room{
				{
					CreateRoom: map[string]interface{}{
						"preset": "public_chat",
					},
					Creator: "@alice",
					Ref:     "alice_room",
					Events: []Event{
						{
							Type: "m.room.message",
							Content: map[string]interface{}{
								"body":    "Hello world",
								"msgtype": "m.text",
							},
							Sender: "@alice",
						},
					},
				},
			},
		},
		{
			Name: "hs2",
			Users: []User{
				{
					Localpart:   "@bob",
					DisplayName: "Bob",
				},
			},
			Rooms: []Room{
				{
					Ref: "alice_room",
					Events: []Event{
						{
							Type:     "m.room.member",
							StateKey: Ptr("@bob:hs2"),
							Content: map[string]interface{}{
								"membership": "join",
							},
							Sender: "@bob",
						},
						{
							Type: "m.room.message",
							Content: map[string]interface{}{
								"body":    "Hello world2",
								"msgtype": "m.text",
							},
							Sender: "@bob",
						},
					},
				},
			},
		},
	},
})
