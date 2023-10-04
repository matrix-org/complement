package b

// BlueprintCleanHS is a clean homeserver with no rooms or users
var BlueprintCleanHS = MustValidate(Blueprint{
	Name: "clean_hs",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
		},
	},
})
