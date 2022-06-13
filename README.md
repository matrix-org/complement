[![Tests](https://github.com/matrix-org/complement/actions/workflows/ci.yaml/badge.svg)](https://github.com/matrix-org/complement/actions/workflows/ci.yaml)
[![Complement Dev](https://img.shields.io/matrix/complement:matrix.org.svg?label=%23complement%3Amatrix.org&logo=matrix&server_fqdn=matrix.org)](https://matrix.to/#/#complement:matrix.org)

# Complement

Complement is a black box integration testing framework for Matrix homeservers.

## Running

You need to have Go and Docker installed, as well as `libolm3` and `libolm-dev`. Then:

```
$ COMPLEMENT_BASE_IMAGE=some-matrix/homeserver-impl go test -v ./tests/...
```

You can install `libolm3` on Debian using something like:
```
echo "deb http://deb.debian.org/debian buster-backports main" > /etc/apt/sources.list.d/complement.list && apt-get update && apt-get install -y libolm3 libolm-dev/buster-backports
```
or on Mac:
```
brew install libolm
```
If you are on an Apple Silicon Mac then you'll need to set these environment variables too so Go can find `libolm`:
```
export LIBRARY_PATH=/opt/homebrew/lib
export CPATH=/opt/homebrew/include
export PATH=/opt/homebrew/bin:$PATH
```

You can either use your own image, or one of the ones supplied in the [dockerfiles](./dockerfiles) directory.

A full list of config options can be found [in the config file](./internal/config/config.go). All normal Go test config
options will work, so to just run 1 named test and include a timeout for the test run:
```
$ COMPLEMENT_BASE_IMAGE=complement-dendrite:latest go test -timeout 30s -run '^(TestOutboundFederationSend)$' -v ./tests/...
```

### Potential conflict with firewall software

The homeserver in the test image needs to be able to make requests to the mock
homeserver hosted by Complement itself, which may be blocked by firewall
software. This will manifest with a subset of the tests (mostly those to do
with federation) inexplicably failing.

To solve this, you will need to configure your firewall to allow such requests.

If you are using [ufw](https://code.launchpad.net/ufw), this can be done with:

```sh
sudo ufw allow in on br-+
```

### Running against Dendrite

For instance, for Dendrite:
```
# build a docker image for Dendrite...
$ git clone https://github.com/matrix-org/dendrite
$ (cd dendrite && docker build -t complement-dendrite -f build/scripts/Complement.Dockerfile .)
# ...and test it
$ COMPLEMENT_BASE_IMAGE=complement-dendrite:latest go test -v ./tests/...
```

### Running against Synapse

If you're looking to run Complement against a local dev instance of Synapse, see [`matrix-org/synapse` -> `scripts-dev/complement.sh`](https://github.com/matrix-org/synapse/blob/develop/scripts-dev/complement.sh).

If you want to develop Complement tests while working on a local dev instance
of Synapse, use the
[`scripts-dev/complement.sh`](https://github.com/matrix-org/synapse/blob/develop/scripts-dev/complement.sh)
script and set the `COMPLEMENT_DIR` environment variable to the filepath of
your local Complement checkout. Arguments to `go test` can be supplied as an argument to the script, e.g.:

```sh
COMPLEMENT_DIR=/path/to/complement scripts-dev/complement.sh -run "TestOutboundFederation(Profile|Send)"
```

To run Complement against a specific release of Synapse, build the
"complement-synapse" image with a `SYNAPSE_VERSION` build argument. For
example:

```sh
(cd synapse && docker build -t complement-synapse:v1.36.0 -f docker/complement/Dockerfile --build-arg=SYNAPSE_VERSION=v1.36.0 docker/complement)
COMPLEMENT_BASE_IMAGE=complement-synapse:v1.36.0 go test ./tests/...
```

### Image requirements

If you're looking to run against a custom Dockerfile, it must meet the following requirements:

- The Dockerfile must `EXPOSE 8008` and `EXPOSE 8448` for client and federation traffic respectively.
- The homeserver should run and listen on these ports.
- The homeserver should become healthy within `COMPLEMENT_SPAWN_HS_TIMEOUT_SECS` if a `HEALTHCHECK` is specified in the Dockerfile.
- The homeserver needs to `200 OK` requests to `GET /_matrix/client/versions`.
- The homeserver needs to manage its own storage within the image.
- The homeserver needs to accept the server name given by the environment variable `SERVER_NAME` at runtime.
- The homeserver needs to assume dockerfile `CMD` or `ENTRYPOINT` instructions will be run multiple times.
- The homeserver needs to use `complement` as the registration shared secret for `/_synapse/admin/v1/register`, if supported. If this endpoint 404s then these tests are skipped.


### Developing locally

If you want to write Complement tests _and_ hack on a homeserver implementation at the same time it can be very awkward
to have to `docker build` the image all the time. To resolve this, Complement support "host mounts" which mount a directory
from the host to the container. This is set via `COMPLEMENT_HOST_MOUNTS`:

```
COMPLEMENT_HOST_MOUNTS='/my/local/dir:/container/dir;/another/local/dir:/container/dir2:ro'

which is:
  - /my/local/dir:/container/dir
  - /another/local/dir:/container/dir2:ro

which is of the form:
  - HOST:CONTAINER[:ro] where :ro makes the mount read-only.
```

For example, for Dendrite: `COMPLEMENT_HOST_MOUNTS='/your/local/dendrite:/dendrite:ro;/your/go/path:/go:ro'`.

### Getting prettier output

The default output isn't particularly nice to read. You can use [gotestfmt](https://github.com/haveyoudebuggedit/gotestfmt)
to make this very pretty. To do so, ask for JSON output via `go test -json` then pipe the output to `gotestfmt`.
If you are doing this in CI, make sure to `set -o pipefail` or else test failures will NOT result in a non-zero exit code
as `gotestfmt`'s exit code (0 as it successfully printed) will replace the previous commands exit code.
See Complement's [Github Actions](https://github.com/matrix-org/complement/blob/master/.github/workflows/ci.yaml) file
for an example of how to do this correctly.

## Writing tests

To get started developing Complement tests, see [the onboarding documentation](ONBOARDING.md).

### Build tags

Complement uses build tags to include or exclude tests for each homeserver. Build tags are comments at the top of the file that look
like:
```go
// +build msc2403
```
We have tags for MSCs (the above is in `msc2403_test.go`) as well as general blacklists for a homeserver implementation e.g Dendrite,
which has the name `dendrite_blacklist`. These are implemented as inverted tags such that specifying the tag results in the file not
being picked up by `go test`. For example, `apidoc_presence_test.go` has:
```go
// +build !dendrite_blacklist
```
and all Dendrite tests run with `-tags="dendrite_blacklist"` to cause this file to be skipped. You can run tests with build tags like this:
```
COMPLEMENT_BASE_IMAGE=complement-synapse:latest go test -v -tags="synapse_blacklist,msc2403" ./tests/...
```
This runs Complement with a Synapse HS and ignores tests which Synapse doesn't implement, and includes tests for MSC2403.

## Why 'Complement'?

Because **M**<sup>*C*</sup> = **1** - **M**

## Complement PKI

As the Matrix federation protocol expects federation endpoints to be served with valid TLS certs,
Complement will create a self-signed CA cert to use for creating valid TLS certs in homeserver containers,
and mount these files onto your homeserver container:
- `/complement/ca/ca.crt`: the public certificate for the CA. The homeserver
  under test should be configured to trust certificates signed by this CA (e.g.
  by adding it to the trusted cert store in `/etc/ca-certificates`).
- `/complement/ca/ca.key`: the CA's private key. This is needed to sign the
  homeserver's certificate.

For example, to sign your certificate for the homeserver, run at each container start (Ubuntu):
```
openssl genrsa -out $SERVER_NAME.key 2048
openssl req -new -sha256 -key $SERVER_NAME.key -subj "/C=US/ST=CA/O=MyOrg, Inc./CN=$SERVER_NAME" -out $SERVER_NAME.csr
openssl x509 -req -in $SERVER_NAME.csr -CA /complement/ca/ca.crt -CAkey /complement/ca/ca.key -CAcreateserial -out $SERVER_NAME.crt -days 1 -sha256
```

To add the CA cert to your trust store (Ubuntu):
```
cp /complement/ca/ca.crt /usr/local/share/ca-certificates/complement.crt
update-ca-certificates
```

## Sytest parity

```
$ go build ./cmd/sytest-coverage
$ ./sytest-coverage -v
10apidoc/01register 3/10 tests
    × GET /register yields a set of flows
    × POST $ep_name admin with shared secret
    × POST $ep_name with shared secret
    × POST $ep_name with shared secret disallows symbols
    × POST $ep_name with shared secret downcases capitals
    × POST /register allows registration of usernames with '$chr'
    ✓ POST /register can create a user
    ✓ POST /register downcases capitals in usernames
    × POST /register rejects registration of usernames with '$q'
    ✓ POST /register returns the same device_id as that in the request

10apidoc/01request-encoding 1/1 tests
    ✓ POST rejects invalid utf-8 in JSON

10apidoc/02login 6/6 tests
    ✓ GET /login yields a set of flows
    ✓ POST /login as non-existing user is rejected
    ✓ POST /login can log in as a user
    ✓ POST /login can log in as a user with just the local part of the id
    ✓ POST /login returns the same device_id as that in the request
    ✓ POST /login wrong password is rejected

10apidoc/04version 1/1 tests
    ✓ Version responds 200 OK with valid structure

10apidoc/10profile-displayname 2/2 tests
    ✓ GET /profile/:user_id/displayname publicly accessible
    ✓ PUT /profile/:user_id/displayname sets my name

10apidoc/11profile-avatar_url 2/2 tests
    ✓ GET /profile/:user_id/avatar_url publicly accessible
    ✓ PUT /profile/:user_id/avatar_url sets my avatar

10apidoc/12device_management 2/8 tests
    × DELETE /device/{deviceId}
    × DELETE /device/{deviceId} requires UI auth user to match device owner
    × DELETE /device/{deviceId} with no body gives a 401
    ✓ GET /device/{deviceId}
    ✓ GET /device/{deviceId} gives a 404 for unknown devices
    × GET /devices
    × PUT /device/{deviceId} gives a 404 for unknown devices
    × PUT /device/{deviceId} updates device fields

10apidoc/13ui-auth 0/4 tests
10apidoc/20presence 2/2 tests
    ✓ GET /presence/:user_id/status fetches initial status
    ✓ PUT /presence/:user_id/status updates my presence

10apidoc/30room-create 10/10 tests
    ✓ Can /sync newly created room
    ✓ POST /createRoom creates a room with the given version
    ✓ POST /createRoom ignores attempts to set the room version via creation_content
    ✓ POST /createRoom makes a private room
    ✓ POST /createRoom makes a private room with invites
    ✓ POST /createRoom makes a public room
    ✓ POST /createRoom makes a room with a name
    ✓ POST /createRoom makes a room with a topic
    ✓ POST /createRoom rejects attempts to create rooms with numeric versions
    ✓ POST /createRoom rejects attempts to create rooms with unknown versions

10apidoc/31room-state 13/13 tests
    ✓ GET /directory/room/:room_alias yields room ID
    ✓ GET /joined_rooms lists newly-created room
    ✓ GET /publicRooms lists newly-created room
    ✓ GET /rooms/:room_id/joined_members fetches my membership
    ✓ GET /rooms/:room_id/state fetches entire room state
    ✓ GET /rooms/:room_id/state/m.room.member/:user_id fetches my membership
    ✓ GET /rooms/:room_id/state/m.room.member/:user_id?format=event fetches my membership event
    ✓ GET /rooms/:room_id/state/m.room.name gets name
    ✓ GET /rooms/:room_id/state/m.room.power_levels fetches powerlevels
    ✓ GET /rooms/:room_id/state/m.room.topic gets topic
    ✓ POST /createRoom with creation content
    ✓ POST /rooms/:room_id/state/m.room.name sets name
    ✓ POST /rooms/:room_id/state/m.room.topic sets topic

10apidoc/32room-alias 2/2 tests
    ✓ GET /rooms/:room_id/aliases lists aliases
    ✓ PUT /directory/room/:room_alias creates alias

10apidoc/33room-members 3/8 tests
    ✓ POST /join/:room_alias can join a room
    × POST /join/:room_alias can join a room with custom content
    ✓ POST /join/:room_id can join a room
    × POST /join/:room_id can join a room with custom content
    × POST /rooms/:room_id/ban can ban a user
    × POST /rooms/:room_id/invite can send an invite
    ✓ POST /rooms/:room_id/join can join a room
    × POST /rooms/:room_id/leave can leave a room

10apidoc/34room-messages 0/5 tests
10apidoc/35room-typing 0/1 tests
10apidoc/36room-levels 0/4 tests
10apidoc/37room-receipts 0/1 tests
10apidoc/38room-read-marker 0/1 tests
10apidoc/40content 0/2 tests
10apidoc/45server-capabilities 0/2 tests
11register 0/7 tests
12login/01threepid-and-password 0/1 tests
12login/02cas 0/3 tests
13logout 0/4 tests
14account/01change-password 7/7 tests
    ✓ After changing password, a different session no longer works by default
    ✓ After changing password, can log in with new password
    ✓ After changing password, can't log in with old password
    ✓ After changing password, different sessions can optionally be kept
    ✓ After changing password, existing session still works
    ✓ Pushers created with a different access token are deleted on password change
    ✓ Pushers created with a the same access token are not deleted on password change

14account/02deactivate 3/4 tests
    × After deactivating account, can't log in with an email
    ✓ After deactivating account, can't log in with password
    ✓ Can deactivate account
    ✓ Can't deactivate account with wrong password

21presence-events 0/2 tests
30rooms/01state 2/7 tests
    × Global initialSync
    × Global initialSync with limit=0 gives no messages
    × Joining room twice is idempotent
    ✓ Room creation reports m.room.create to myself
    ✓ Room creation reports m.room.member to myself
    × Setting room topic reports m.room.topic to myself
    × Setting state twice is idempotent

30rooms/02members-local 0/3 tests
30rooms/03members-remote 0/5 tests
30rooms/04messages 0/9 tests
30rooms/05aliases 6/13 tests
    ✓ Canonical alias can be set
    ✓ Canonical alias can include alt_aliases
    ✓ Alias creators can delete alias with no ops
    ✓ Alias creators can delete canonical alias with no ops
    × Can delete canonical alias
    ✓ Deleting a non-existent alias should return a 404
    ✓ Only room members can list aliases of a room
    × Regular users can add and delete aliases in the default room configuration
    × Regular users can add and delete aliases when m.room.aliases is restricted
    × Remote room alias queries can handle Unicode
    × Room aliases can contain Unicode
    × Users can't delete other's aliases
    × Users with sufficient power-level can delete other's aliases

30rooms/06invite 1/13 tests
    × Can invite users to invite-only rooms
    ✓ Test that we can be reinvited to a room we created
    × Invited user can reject invite
    × Invited user can reject invite for empty room
    × Invited user can reject invite over federation
    × Invited user can reject invite over federation for empty room
    × Invited user can reject invite over federation several times
    × Invited user can reject local invite after originator leaves
    × Invited user can see room metadata
    × Remote invited user can see room metadata
    × Uninvited users cannot join the room
    × Users cannot invite a user that is already in the room
    × Users cannot invite themselves to a room

30rooms/07ban 0/2 tests
30rooms/08levels 0/3 tests
30rooms/09eventstream 0/2 tests
30rooms/10redactions 0/5 tests
30rooms/11leaving 0/5 tests
30rooms/12thirdpartyinvite 0/13 tests
30rooms/13guestaccess 0/11 tests
30rooms/14override-per-room 0/2 tests
30rooms/15kick 0/2 tests
30rooms/20typing 0/3 tests
30rooms/21receipts 0/2 tests
30rooms/22profile 0/1 tests
30rooms/30history-visibility 0/2 tests
30rooms/31forget 0/5 tests
30rooms/32erasure 0/1 tests
30rooms/40joinedapis 0/2 tests
30rooms/50context 0/4 tests
30rooms/51event 3/3 tests
    ✓ /event/ does not allow access to events before the user joined
    ✓ /event/ on joined room works
    ✓ /event/ on non world readable room does not work

30rooms/52members 0/3 tests
30rooms/60version_upgrade 0/19 tests
30rooms/70publicroomslist 0/5 tests
31sync/01filter 2/2 tests
    ✓ Can create filter
    ✓ Can download filter

31sync/02sync 0/1 tests
31sync/03joined 0/6 tests
31sync/04timeline 0/9 tests
31sync/05presence 0/3 tests
31sync/06state 0/14 tests
31sync/07invited 0/3 tests
31sync/08polling 0/2 tests
31sync/09archived 0/8 tests
31sync/10archived-ban 0/3 tests
31sync/11typing 0/3 tests
31sync/12receipts 0/2 tests
31sync/13filtered_sync 0/2 tests
31sync/14read-markers 0/3 tests
31sync/15lazy-members 0/11 tests
31sync/16room-summary 0/4 tests
31sync/17peeking 0/4 tests
32room-versions 0/6 tests
40presence 0/4 tests
41end-to-end-keys/01-upload-key 0/6 tests
41end-to-end-keys/03-one-time-keys 0/1 tests
41end-to-end-keys/04-query-key-federation 0/1 tests
41end-to-end-keys/05-one-time-key-federation 0/1 tests
41end-to-end-keys/06-device-lists 0/15 tests
41end-to-end-keys/07-backup 0/10 tests
41end-to-end-keys/08-cross-signing 0/8 tests
42tags 0/7 tests
43search 0/5 tests
44account_data 0/6 tests
45openid 0/3 tests
46direct/01directmessage 0/3 tests
46direct/02reliability 0/2 tests
46direct/03polling 0/1 tests
46direct/04federation 0/2 tests
46direct/05wildcard 0/4 tests
48admin 0/5 tests
49ignore 0/3 tests
50federation/00prepare 0/1 tests
50federation/01keys 1/4 tests
    ✓ Federation key API allows unsigned requests for keys
    × Federation key API can act as a notary server via a $method request
    × Key notary server must not overwrite a valid key with a spurious result from the origin server
    × Key notary server should return an expired key if it can't find any others

50federation/02server-names 0/1 tests
50federation/10query-profile 1/2 tests
    × Inbound federation can query profile data
    ✓ Outbound federation can query profile data

50federation/11query-directory 0/2 tests
50federation/30room-join 0/19 tests
50federation/31room-send 0/5 tests
50federation/32room-getevent 0/2 tests
50federation/33room-get-missing-events 1/4 tests
    × Inbound federation can return missing events for $vis visibility
    × Outbound federation can request missing events
    ✓ Outbound federation will ignore a missing event with bad JSON for room version 6
    × outliers whose auth_events are in a different room are correctly rejected

50federation/34room-backfill 0/5 tests
50federation/35room-invite 0/11 tests
50federation/36state 0/11 tests
50federation/37public-rooms 0/1 tests
50federation/38receipts 0/2 tests
50federation/39redactions 0/4 tests
50federation/40devicelists 0/7 tests
50federation/40publicroomlist 0/1 tests
50federation/41power-levels 0/2 tests
50federation/43typing 0/1 tests
50federation/44presence 0/1 tests
50federation/50no-deextrem-outliers 0/1 tests
50federation/50server-acl-endpoints 0/1 tests
50federation/51transactions 0/2 tests
50federation/52soft-fail 0/3 tests
51media/01unicode 0/5 tests
51media/02nofilename 3/3 tests
    ✓ Can download without a file name locally
    ✓ Can download without a file name over federation
    ✓ Can upload without a file name

51media/03ascii 0/5 tests
51media/10thumbnail 0/2 tests
51media/20urlpreview 0/1 tests
51media/30config 0/1 tests
52user-directory/01public 0/7 tests
52user-directory/02private 0/3 tests
54identity 0/6 tests
60app-services/01as-create 0/7 tests
60app-services/02ghost 0/6 tests
60app-services/03passive 0/3 tests
60app-services/04asuser 0/2 tests
60app-services/05lookup3pe 0/4 tests
60app-services/06publicroomlist 0/2 tests
60app-services/07deactivate 0/1 tests
61push/01message-pushed 0/7 tests
61push/02add_rules 0/7 tests
61push/03_unread_count 0/2 tests
61push/05_set_actions 0/4 tests
61push/06_get_pusher 0/1 tests
61push/06_push_rules_in_sync 0/4 tests
61push/07_set_enabled 0/2 tests
61push/08_rejected_pushers 0/1 tests
61push/09_notifications_api 0/1 tests
61push/80torture 0/3 tests
80torture/03events 1/1 tests
    ✓ Event size limits

80torture/10filters 1/1 tests
    ✓ Check creating invalid filters returns 4xx

80torture/20json 3/3 tests
    ✓ Invalid JSON floats
    ✓ Invalid JSON integers
    ✓ Invalid JSON special values

90jira/SYN-205 1/1 tests
    ✓ Rooms can be created with an initial invite list (SYN-205)

90jira/SYN-328 0/1 tests
90jira/SYN-343 1/1 tests
    ✓ Non-present room members cannot ban others

90jira/SYN-390 1/1 tests
    ✓ Getting push rules doesn't corrupt the cache SYN-390

90jira/SYN-516 0/1 tests
90jira/SYN-627 0/1 tests

TOTAL: 85/622 tests converted
```
