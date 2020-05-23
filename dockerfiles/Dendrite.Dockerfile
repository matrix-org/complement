FROM golang:1.13-stretch as build

ARG dendrite-source
WORKDIR /build
# You have to run this inside the dendrite repo
COPY . .
RUN go build ./cmd/dendrite-monolith-server

FROM golang:1.13-stretch

RUN apt-get update && apt-get install sqlite3

WORKDIR /server
COPY matrix_key.pem .
COPY server.crt .
COPY server.key .
COPY complement-dendrite.yaml dendrite.yaml
COPY --from=build /build/dendrite-monolith-server .

EXPOSE 8008 8448
CMD ./dendrite-monolith-server
