package b

// BlueprintAlice is a single user homeserver
var BlueprintAlice = MustValidate(Blueprint{
	Name: "alice",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
			Users: []User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
				},
			},
		},
	},
})
