FROM golang:1.13-stretch as build
RUN apt-get update && apt-get install sqlite3
WORKDIR /build

ADD https://github.com/matrix-org/dendrite/archive/master.tar.gz /build/master.tar.gz
RUN tar xvfz master.tar.gz
WORKDIR /build/dendrite-master
RUN go build ./cmd/dendrite-monolith-server
RUN go build ./cmd/generate-keys
RUN go build ./cmd/generate-config
RUN ./generate-config > dendrite.yaml
RUN ./generate-keys --private-key matrix_key.pem --tls-cert server.crt --tls-key server.key

ENV SERVER_NAME=localhost
EXPOSE 8008 8448

CMD sed -i "s/server_name: localhost/server_name: ${SERVER_NAME}/g" dendrite.yaml && sed -i "s/disable_tls_validation: false/disable_tls_validation: true/g" dendrite.yaml && ./dendrite-monolith-server --tls-cert server.crt --tls-key server.key --config dendrite.yaml
