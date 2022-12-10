# This dockerfile simply holds `homerunner` when run in a containerized env.
# see matrix-org/complement/dockerfiles for links to the complement
# images that are used in tests.

# NB: building needs to be done from the root of the complement directory, ie
# `~/work/complement> docker build -t cmd/homerunner/Dockerfile . `
# to ensure the whole project is passed to the docker container to build.

# Build

FROM golang:1.19-buster
RUN echo "deb http://deb.debian.org/debian buster-backports main" > /etc/apt/sources.list.d/complement.list
RUN apt-get update && apt-get install -y libolm3 libolm-dev/buster-backports

WORKDIR /app
COPY . /app

RUN go build ./cmd/homerunner

# Executable

FROM debian:buster
RUN echo "deb http://deb.debian.org/debian buster-backports main" > /etc/apt/sources.list.d/complement.list
RUN apt-get update && apt-get install -y libolm3 && apt-get clean

COPY --from=0 /app/homerunner /usr/local/bin/homerunner

HEALTHCHECK --interval=1m --timeout=5s \
  CMD curl -f http://localhost:54321/ || exit 1


EXPOSE 54321/tcp
ENTRYPOINT ["/usr/local/bin/homerunner"]

