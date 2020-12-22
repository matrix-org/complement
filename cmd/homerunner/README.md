### Homerunner

An HTTP server which can spin up homeservers and execute blueprints on them. Powered by the same internals as Complement.

Environment variables:
```
HOMERUNNER_LIFETIME_MINS=30                                       # how long networks can exist for before being destroyed
HOMERUNNER_PORT=54321                                             # port to listen on
HOMERUNNER_VER_CHECK_ITERATIONS=100                               # how long to wait for the base image to spin up
HOMERUNNER_KEEP_BLUEPRINTS='clean_hs federation_one_to_one_room'  # space delimited blueprint names to keep images for
```


Create a homeserver network:
```
$ curl -XPOST -d '{"base_image_uri":"complement-dendrite", "blueprint_name":"federation_one_to_one_room"}' http://localhost:54321/create
{
	"homeservers": {
		"hs1": {
			"BaseURL": "http://localhost:32829",
			"FedBaseURL": "https://localhost:32828",
			"ContainerID": "980cb5310c37a7c7823dfe6968139b12774f39e2a5641e0aa011dcdf1a34f686",
			"AccessTokens": {
				"@alice:hs1": "z7EdGeM9BRJVwKQOs5SLJyAEflwWTejmJ5YcnJyJMVk"
			}
		},
		"hs2": {
			"BaseURL": "http://localhost:32827",
			"FedBaseURL": "https://localhost:32826",
			"ContainerID": "d926131d13bfaf5ee226fa90de742b529ffe9e965071883af15224f33d8d38f8",
			"AccessTokens": {
				"@bob:hs2": "vjuuiSMLce7kpItdIm0zHGAjaxYCt5ICfZEMX1Ba1MI"
			}
		}
	},
	"expires": "2020-12-22T16:22:28.99267Z"
}
```

Destroy the network:
```
curl -XPOST -d '{"blueprint_name":"federation_one_to_one_room"}' http://localhost:54321/destroy                                       
{}
```
