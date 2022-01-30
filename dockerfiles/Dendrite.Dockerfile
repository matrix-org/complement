FROM golang:1.16-stretch as build
RUN apt-get update && apt-get install -y sqlite3
WORKDIR /build

# pull files from branches
ARG BRANCH=master
ADD https://github.com/matrix-org/dendrite/archive/$BRANCH.tar.gz /build/dendrite.tar.gz

# strip the top-level directory which has the name of the branch in it
RUN tar --strip=1 -xzf dendrite.tar.gz
RUN go build ./cmd/dendrite-monolith-server
RUN go build ./cmd/generate-keys
RUN go build ./cmd/generate-config
RUN ./generate-config --ci > dendrite.yaml
RUN ./generate-keys --private-key matrix_key.pem --tls-cert server.crt --tls-key server.key

ENV SERVER_NAME=localhost
EXPOSE 8008 8448

CMD sed -i "s/server_name: localhost/server_name: ${SERVER_NAME}/g" dendrite.yaml && ./dendrite-monolith-server --tls-cert server.crt --tls-key server.key --config dendrite.yaml
