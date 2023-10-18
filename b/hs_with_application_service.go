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
				{
					Localpart:   "@bob",
					DisplayName: "Bob",
				},
			},
			ApplicationServices: []map[string]interface{}{
				{
					"id":               "my_as_id",
					"url":              "http://localhost:9000",
					"sender_localpart": "the-bridge-user",
					"rate_limited":     false,
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
