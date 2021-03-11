### Homerunner

An HTTP server which can spin up homeservers and execute blueprints on them. Powered by the same internals as Complement.

Environment variables (all are optional):
```
HOMERUNNER_LIFETIME_MINS=30                                       # how long networks can exist for before being destroyed
HOMERUNNER_PORT=54321                                             # port to listen on
HOMERUNNER_VER_CHECK_ITERATIONS=100                               # how long to wait for the base image to spin up
HOMERUNNER_KEEP_BLUEPRINTS='clean_hs federation_one_to_one_room'  # space delimited blueprint names to keep images for
HOMERUNNER_SNAPSHOT_BLUEPRINT=/some/file.json                     # single shot execute this blueprint then commit the image, does not run the server
```

To build and run:
```
go build ./cmd/homerunner
./homerunner
```

There are three ways to use Homerunner which are shown below.

#### Deploy an image from Docker

If you have a pre-committed image already (e.g you have an anonymous account snapshot) then run Homerunner like this:
```
HOMERUNNER_KEEP_BLUEPRINTS='name-of-blueprint' ./homerunner
```
This is neccessary to stop Homerunner from cleaning up the image. Then perform a single POST request:
```
curl -XPOST -d '{"blueprint_name":"name-of-blueprint"}'
{
  "homeservers":{
    "hs1":{
      "BaseURL":"http://localhost:55278",
      "FedBaseURL":"https://localhost:55277",
      "ContainerID":"59c6cd9eeb624c5eab7d317fcef63a6e50f98aeb5a6f09cc4b74297bfecb9211",
      "AccessTokens":{
        "@anon-2:hs1":"9VewRcVTCaZ2qnbRN66Aji8l6i-dVlmlLDx44vRUQVI"
      },
      "ApplicationServices":{}
    }
  },
  "expires":"2021-03-09T18:02:37.818757Z"
}
```

#### Deploy an in-line blueprint

*Requires: A base image from [dockerfiles](https://github.com/matrix-org/complement/tree/master/dockerfiles)*

If you have your own custom blueprint you want to run, perform a single POST request:
```
{
  "base_image_uri": "complement-dendrite",
  "blueprint": {
    "Name": "my_custom_blueprint",
    "Homeservers": [
      {
        "Name": "hs1",
        "Users": [
          {
            "Localpart": "anon-64",
            "DisplayName": "anon-64"
	  }
	],
	"Rooms": [ ... ]
      }
    ]
  }
}
```
The format of `blueprint` is the same as the `Blueprint` struct in https://github.com/matrix-org/complement/blob/master/internal/b/blueprints.go#L39

#### Deploy a blueprint from Complement

*Requires: A base image from [dockerfiles](https://github.com/matrix-org/complement/tree/master/dockerfiles)*

This allows you to deploy any one of the static blueprints in https://github.com/matrix-org/complement/tree/master/internal/b

Perform a single POST request:

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
