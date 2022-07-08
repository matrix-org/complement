#!/bin/bash -eu

export HOMERUNNER_PORT=5544
export HOMERUNNER_SPAWN_HS_TIMEOUT_SECS=20

# build and run homerunner
go build ..
echo 'Running homerunner'
./homerunner &
HOMERUNNER_PID=$!
# knife homerunner when this script finishes
trap "kill $HOMERUNNER_PID" EXIT
sleep 5 # wait for homerunner to be listening

# build and run the test
echo 'Running tests'
yarn install
node test.mjs
