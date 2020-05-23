### Complement

Complement is a black box integration testing framework for Matrix homeservers.

#### Running

You need to have Docker installed. Then:
```
$ go build ./cmd/complement
$ COMPLEMENT_BASE_IMAGE=some-matrix/homeserver-impl COMPLEMENT_BASE_IMAGE_ARGS='-foo bar -baz 1' ./complement
```
A full list of config options can be found in the config file.

##### Image requirements
- The Dockerfile must `EXPOSE 8008` and `EXPOSE 8448` for client and federation traffic respectively.
- The homeserver should run and listen on these ports.
- The homeserver needs to `200 OK` requests to `GET /_matrix/client/versions`.
- The homeserver needs to manage its own storage within the image.

For example dockerfiles, see [./dockerfiles](this directory).