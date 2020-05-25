FROM golang:1.13-stretch as build
RUN apt-get update && apt-get install sqlite3
WORKDIR /build

ADD https://github.com/matrix-org/dendrite/archive/master.tar.gz /build/master.tar.gz
RUN tar xvfz master.tar.gz
WORKDIR /build/dendrite-master
RUN go build ./cmd/dendrite-monolith-server
RUN go build ./cmd/generate-keys
RUN ./generate-keys --private-key matrix_key.pem --tls-cert server.crt --tls-key server.key
COPY dendrite.yaml dendrite.yaml

ENV SERVER_NAME=localhost
EXPOSE 8008 8448

CMD sed -i "s/SERVER_NAME/${SERVER_NAME}/g" dendrite.yaml && ./dendrite-monolith-server --tls-cert server.crt --tls-key server.key --config dendrite.yaml
