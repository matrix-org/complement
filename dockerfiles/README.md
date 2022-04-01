This directory contains a list of dockerfiles which can be used with Complement.

This used to have stand-alone Dockerfiles which would pull sources directly and build them for testing.
However, this doesn't work nicely when running Complement with local checkouts, so homeservers would
end up copying the Dockerfiles in this directory to their own repository. In an effort to reduce
duplication, we now point to dockerfiles in respective repositories rather than have them directly here.

- Dendrite: https://github.com/matrix-org/dendrite/blob/v0.6.3/build/scripts/Complement.Dockerfile
- Synapse: https://github.com/matrix-org/synapse/blob/v1.52.0/scripts-dev/complement.sh
