FROM docker.io/library/golang:1.18-alpine as build
WORKDIR /root/project
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY . .
RUN go build -v -o /bin/app

FROM docker.io/library/docker
RUN apk update && apk add git
WORKDIR /root/project
COPY . .
WORKDIR /root/complement
COPY --from=build /bin/app /bin/app
ENTRYPOINT /bin/app
