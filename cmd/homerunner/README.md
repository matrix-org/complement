## Homerunner

An HTTP server which can spin up homeservers and execute blueprints on them. Powered by the same internals as Complement.

Environment variables (all are optional):
```
HOMERUNNER_LIFETIME_MINS=30                                       # how long networks can exist for before being destroyed
HOMERUNNER_PORT=54321                                             # port to listen on
HOMERUNNER_SPAWN_HS_TIMEOUT_SECS=5                                # how long to wait for the base image to spin up
HOMERUNNER_KEEP_BLUEPRINTS='clean_hs federation_one_to_one_room'  # space delimited blueprint names to keep images for
HOMERUNNER_SNAPSHOT_BLUEPRINT=/some/file.json                     # single shot execute this blueprint then commit the image, does not run the server
```

To build and run:
```
go build ./cmd/homerunner
./homerunner
```

There are three ways to use Homerunner which are shown below.

### Deploy an image from Docker

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

To find the blueprint name, run `docker inspect --format={{.ContainerConfig.Labels.complement_blueprint}} your-image:latest`

### Deploy an in-line blueprint

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

### Deploy a blueprint from Complement

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

### Creating pre-committed images

If you have a blueprint (e.g from [account-snapshot](https://github.com/matrix-org/complement/tree/master/cmd/account-snapshot)) which you wish to snapshot into a docker image, then run this command:
```
HOMERUNNER_SNAPSHOT_BLUEPRINT=/some/file.json ./homerunner
```
The data in `/some/file` must be the same as an in-line blueprint request. The output will be an image in `docker image ls` along the lines of:
```
REPOSITORY                                                             TAG                     IMAGE ID       CREATED         SIZE
localhost/complement                                                   snapshot_anon2hs1.hs1   1b18395c0d1e   37 hours ago    330MB
```
If you run `docker inspect repo:tag` on this you will see a section called `Labels`:
```
"Labels": {
  "access_token_@anon-2:hs1": "tcy4A0xsHH0_iKq4O7AlOdg4zdThDyw7R8lUFjoZO4s",
  "complement_blueprint": "snapshot_anon2hs1",
  "complement_context": "snapshot_anon2hs1.hs1",
  "complement_hs_name": "hs1"
}
```
The `complement_blueprint` label is the blueprint name you should use to deploy this image. You can now push this image to docker/gitlab.


### Access tokens

Access tokens are returned when deploying the blueprint but sometimes you want to login as a normal user. The format for passwords for all users created by Complement is [here](https://github.com/matrix-org/complement/blob/fc87b081ac9dd3c8e52bcd2ed155bc8d49ce6d56/internal/instruction/runner.go#L415).

### Health

Homerunner will respond to `GET /health` with a 200 response. You can use this to check if homerunner is ready when running your tests.


### Running using dind (docker-in-docker)

The provided Docker container contains just homerunner, and a separate docker daemon will be required
to host the homeservers that are started.

This can be done by mounting the unix socket into the container, or running a `docker:dind` sidecar and exporting `DOCKER_HOST` correctly (eg `tcp://localhost:2375/`).

