package b

// BlueprintHSWithApplicationService who has an application service to interact with
var BlueprintHSWithApplicationService = MustValidate(Blueprint{
	Name: "hs_with_application_service",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
			Users: []User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
				},
			},
			ApplicationServices: []ApplicationService{
				{
					ID:              "my_as_id",
					SenderLocalpart: "the-bridge-user",
					RateLimited:     false,
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
