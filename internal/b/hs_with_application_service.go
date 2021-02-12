package b

// BlueprintHSWithApplicationService who has an application service to interact with
var BlueprintHSWithApplicationService = MustValidate(Blueprint{
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
			ApplicationServices: []ApplicationService{
				{
					ID:              "my-as-id",
					URL:             "http://localhost:9000",
					SenderLocalpart: "the-bridge-user",
					RateLimited:     false,
				},
			},
		},
	},
})
