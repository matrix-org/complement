package b

var BlueprintOneToOneRoom = MustValidate(Blueprint{
	Name: "one_to_one_room",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
			Users: []User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
				},
				{
					Localpart:   "@bob",
					DisplayName: "Bob",
				},
			},
			Rooms: []Room{
				{
					CreateRoom: map[string]interface{}{
						"preset": "public_chat",
					},
					Creator: "@alice",
					Events: []Event{
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
					},
				},
			},
		},
	},
})
