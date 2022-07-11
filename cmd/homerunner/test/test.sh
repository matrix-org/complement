#!/bin/bash -eu

export HOMERUNNER_PORT=5544
export HOMERUNNER_SPAWN_HS_TIMEOUT_SECS=30

# build and run homerunner
go build ..
echo 'Running homerunner'
./homerunner &
HOMERUNNER_PID=$!
# knife homerunner when this script finishes
trap "kill $HOMERUNNER_PID" EXIT

# wait for homerunner to be listening, we want this endpoint to 404 instead of connrefused
until [ \
  "$(curl -s -w '%{http_code}' -o /dev/null "http://localhost:${HOMERUNNER_PORT}/idonotexist")" \
  -eq 404 ]
do
  echo 'Waiting for homerunner to start...'
  sleep 1
done

# build and run the test
echo 'Running tests'
yarn install
node test.mjs
