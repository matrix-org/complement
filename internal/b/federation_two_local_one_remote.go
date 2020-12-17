package b

// BlueprintFederationTwoLocalOneRemote is a two-user homeserver federating with a one-user homeserver
var BlueprintFederationTwoLocalOneRemote = MustValidate(Blueprint{
	Name: "federation_two_local_one_remote",
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
		{
			Name: "hs2",
			Users: []User{
				{
					Localpart:   "@charlie",
					DisplayName: "Charlie",
				},
			},
		},
	},
})
