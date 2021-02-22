#!/bin/bash

set -e

sed -i "s/SERVER_NAME/${SERVER_NAME}/g" /conf/homeserver.yaml

for as_id in $AS_REGISTRATION_IDS
do
  # Insert the path to the registration file and the AS_REGISTRATION_FILES marker in order 
  # to add other application services in the next iteration of the loop
  sed -i "s/AS_REGISTRATION_FILES/  - \/conf\/${as_id}.yaml\nAS_REGISTRATION_FILES/g" /conf/homeserver.yaml
done
# Remove the AS_REGISTRATION_FILES entry
sed -i "s/AS_REGISTRATION_FILES//g" /conf/homeserver.yaml


# generate an ssl cert for the server, signed by our dummy CA
openssl req -new -key /conf/server.tls.key -out /conf/server.tls.csr \
  -subj "/CN=${SERVER_NAME}"
openssl x509 -req -in /conf/server.tls.csr \
  -CA /ca/ca.crt -CAkey /ca/ca.key -set_serial 1 \
  -out /conf/server.tls.crt

exec python -m synapse.app.homeserver -c /conf/homeserver.yaml "$@"

