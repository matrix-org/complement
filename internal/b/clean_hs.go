package b

var BlueprintCleanHS = MustValidate(Blueprint{
	Name: "clean_hs",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
		},
	},
})
