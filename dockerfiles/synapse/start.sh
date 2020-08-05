#!/bin/sh

set -e

sed -i "s/SERVER_NAME/${SERVER_NAME}/g" /conf/homeserver.yaml

# generate an ssl cert for the server, signed by our dummy CA
openssl req -new -key /conf/server.tls.key -out /conf/server.tls.csr \
  -subj "/CN=${SERVER_NAME}"
openssl x509 -req -in /conf/server.tls.csr \
  -CA /ca/ca.crt -CAkey /ca/ca.key -set_serial 1 \
  -out /conf/server.tls.crt

exec python -m synapse.app.homeserver -c /conf/homeserver.yaml "$@"

