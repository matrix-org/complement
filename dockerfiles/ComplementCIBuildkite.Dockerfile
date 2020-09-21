# This Dockerfile is designed for Buildkite and is not a general purpose Complement image.
# Do not rely on this.
#
# This Dockerfile prepares an image with Complement/Docker pre-installed.
# This allows users of this image to issue `docker build` commands to build their HS
# and then run Complement against it.
FROM golang:1.15-buster
RUN curl -fsSL https://get.docker.com -o get-docker.sh && sh get-docker.sh
ADD https://github.com/matrix-org/complement/archive/master.tar.gz .
RUN tar -xzf master.tar.gz && cd complement-master && go mod download 

VOLUME [ "/ca" ]