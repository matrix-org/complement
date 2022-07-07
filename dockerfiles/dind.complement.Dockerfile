FROM docker.io/library/golang:1.18
RUN echo "deb http://deb.debian.org/debian buster-backports main" > /etc/apt/sources.list.d/complement.list \
    && apt-get update \
    && apt-get install -y libolm3 libolm-dev/buster-backports

WORKDIR /root/complement
COPY go.mod .
COPY go.sum .
RUN go mod download
CMD go test -v ./tests/...
