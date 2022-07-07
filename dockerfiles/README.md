This directory contains a list of dockerfiles which can be used with Complement.

This used to have stand-alone Dockerfiles which would pull sources directly and build them for testing.
However, this doesn't work nicely when running Complement with local checkouts, so homeservers would
end up copying the Dockerfiles in this directory to their own repository. In an effort to reduce
duplication, we now point to dockerfiles in respective repositories rather than have them directly here.

- Dendrite: https://github.com/matrix-org/dendrite/blob/v0.8.2/build/scripts/Complement.Dockerfile
- Synapse: https://github.com/matrix-org/synapse/blob/develop/docker/complement/Dockerfile

# MessageHub
Build:
```
docker compose build
docker compose --profile complement build
```

Run:
```
docker compose run --rm complement
```

Example `complement/.env` file:
```
COMPLEMENT_BASE_IMAGE=registry:5000/complement-messagehub
COMPLEMENT_DEBUG=1
COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS=1
COMPLEMENT_SPAWN_HS_TIMEOUT_SECS=5
```
