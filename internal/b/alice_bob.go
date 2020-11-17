package b

// BlueprintAliceBob is a two-user homeserver
var BlueprintAliceBob = MustValidate(Blueprint{
	Name: "alice_bob",
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
