package b

// BlueprintAliceAndBob is a two user homeserver
var BlueprintAliceAndBob = MustValidate(Blueprint{
	Name: "alice_and_bob",
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
		},
	},
})
