### Complement

Complement is a black box integration testing framework for Matrix homeservers.

#### Running

You need to have Go and Docker installed. Then:

```
$ COMPLEMENT_BASE_IMAGE=some-matrix/homeserver-impl COMPLEMENT_BASE_IMAGE_ARGS='-foo bar -baz 1' go test -v ./tests
```

You can either use your own image, or one of the ones supplied in the [dockerfiles](./dockerfiles) directory.

For instance, for Dendrite:
```
# build a docker image for Dendrite...
$ (cd dockerfiles && docker build -t complement-dendrite -f Dendrite.Dockerfile .)
# ...and test it
$ COMPLEMENT_BASE_IMAGE=complement-dendrite:latest go test -v ./tests
```

A full list of config options can be found [in the config file](./internal/config/config.go). All normal Go test config
options will work, so to just run 1 named test and include a timeout for the test run:
```
$ COMPLEMENT_BASE_IMAGE=complement-dendrite:latest go test -timeout 30s -run '^(TestOutboundFederationSend)$' -v ./tests
```

##### Image requirements
- The Dockerfile must `EXPOSE 8008` and `EXPOSE 8448` for client and federation traffic respectively.
- The homeserver should run and listen on these ports.
- The homeserver needs to `200 OK` requests to `GET /_matrix/client/versions`.
- The homeserver needs to manage its own storage within the image.
- The homeserver needs to accept the server name given by the environment variable `SERVER_NAME` at runtime.
- The homeserver can use the CA certificate mounted at /ca to create its own TLS cert (see [Complement PKI](README.md#complement-pki)).

#### Why 'Complement'?

Because **M**<sup>*C*</sup> = **1** - **M**

#### Complement PKI

As the Matrix federation protocol expects federation endpoints to be served with valid TLS certs,
Complement will create a self-signed CA cert to use for creating valid TLS certs in homeserver containers.

To enable it pass `COMPLEMENT_CA=true` to complement or the docker container.
If not used, the homeserver needs to not validate the cert when federating.
To check whether complements runs in PKI mode, `COMPLEMENT_CA` is passed through to the homeserver containers.

The public key to add to the trusted cert store (e.g., /etc/ca-certificates) is mounted at: `/ca/ca.crt`
The private key to sign the created TLS cert is mounted at: `/ca/ca.key`

For example, to sign your certificate for the homeserver, run at each container start (Ubuntu):
```
openssl genrsa -out $SERVER_NAME.key 2048
openssl req -new -sha256 -key $SERVER_NAME.key -subj "/C=US/ST=CA/O=MyOrg, Inc./CN=$SERVER_NAME" -out $SERVER_NAME.csr
openssl x509 -req -in $SERVER_NAME.csr -CA /ca/ca.crt -CAkey /ca/ca.key -CAcreateserial -out $SERVER_NAME.crt -days 1 -sha256
```

To add the CA cert to your trust store (Ubuntu):
```
cp /root/ca.crt /usr/local/share/ca-certificates/complement.crt
update-ca-certificates
```

#### Sytest parity

```
$ go run sytest_coverage.go -v

10apidoc/01register 3/9 tests
    × GET /register yields a set of flows
    ✓ POST /register can create a user
    ✓ POST /register downcases capitals in usernames
    ✓ POST /register returns the same device_id as that in the request
    × POST /register rejects registration of usernames with '$q'
    × POST $ep_name with shared secret
    × POST $ep_name admin with shared secret
    × POST $ep_name with shared secret downcases capitals
    × POST $ep_name with shared secret disallows symbols

10apidoc/01request-encoding 0/1 tests
10apidoc/02login 0/6 tests
10apidoc/03events-initial 0/2 tests
10apidoc/04version 0/1 tests
10apidoc/10profile-displayname 0/2 tests
10apidoc/11profile-avatar_url 0/2 tests
10apidoc/12device_management 0/8 tests
10apidoc/13ui-auth 0/4 tests
10apidoc/20presence 0/2 tests
10apidoc/30room-create 0/10 tests
10apidoc/31room-state 0/14 tests
10apidoc/32room-alias 0/2 tests
10apidoc/33room-members 0/8 tests
10apidoc/34room-messages 0/5 tests
10apidoc/35room-typing 0/1 tests
10apidoc/36room-levels 0/4 tests
10apidoc/37room-receipts 0/1 tests
10apidoc/38room-read-marker 0/1 tests
10apidoc/40content 0/2 tests
10apidoc/45server-capabilities 0/2 tests
11register 0/8 tests
12login/01threepid-and-password 0/1 tests
12login/02cas 0/3 tests
13logout 0/4 tests
14account/01change-password 0/7 tests
14account/02deactivate 0/4 tests
21presence-events 0/3 tests
30rooms/01state 2/9 tests
    ✓ Room creation reports m.room.create to myself
    ✓ Room creation reports m.room.member to myself
    × Setting room topic reports m.room.topic to myself
    × Global initialSync
    × Global initialSync with limit=0 gives no messages
    × Room initialSync
    × Room initialSync with limit=0 gives no messages
    × Setting state twice is idempotent
    × Joining room twice is idempotent

30rooms/02members-local 0/5 tests
30rooms/03members-remote 0/8 tests
30rooms/04messages 0/9 tests
30rooms/05aliases 0/13 tests
30rooms/06invite 0/12 tests
30rooms/07ban 0/2 tests
30rooms/08levels 0/3 tests
30rooms/09eventstream 0/2 tests
30rooms/10redactions 0/5 tests
30rooms/11leaving 0/7 tests
30rooms/12thirdpartyinvite 0/13 tests
30rooms/13guestaccess 0/13 tests
30rooms/14override-per-room 0/2 tests
30rooms/15kick 0/2 tests
30rooms/20typing 0/3 tests
30rooms/21receipts 0/3 tests
30rooms/22profile 0/1 tests
30rooms/30history-visibility 0/2 tests
30rooms/31forget 0/5 tests
30rooms/32erasure 0/1 tests
30rooms/40joinedapis 0/2 tests
30rooms/50context 0/4 tests
30rooms/51event 0/3 tests
30rooms/52members 0/3 tests
30rooms/60version_upgrade 0/19 tests
30rooms/70publicroomslist 0/5 tests
31sync/01filter 0/2 tests
31sync/02sync 0/1 tests
31sync/03joined 0/6 tests
31sync/04timeline 0/8 tests
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
32room-versions 0/6 tests
40presence 0/5 tests
41end-to-end-keys/01-upload-key 0/5 tests
41end-to-end-keys/03-one-time-keys 0/1 tests
41end-to-end-keys/04-query-key-federation 0/1 tests
41end-to-end-keys/05-one-time-key-federation 0/1 tests
41end-to-end-keys/06-device-lists 0/15 tests
41end-to-end-keys/07-backup 0/10 tests
41end-to-end-keys/08-cross-signing 0/8 tests
42tags 0/10 tests
43search 0/5 tests
44account_data 0/10 tests
45openid 0/3 tests
46direct/01directmessage 0/3 tests
46direct/02reliability 0/2 tests
46direct/03polling 0/1 tests
46direct/04federation 0/2 tests
46direct/05wildcard 0/4 tests
48admin 0/5 tests
49ignore 0/3 tests
50federation/00prepare 0/2 tests
50federation/01keys 1/4 tests
    ✓ Federation key API allows unsigned requests for keys
    × Federation key API can act as a notary server via a $method request
    × Key notary server should return an expired key if it can't find any others
    × Key notary server must not overwrite a valid key with a spurious result from the origin server

50federation/02server-names 0/1 tests
50federation/10query-profile 1/2 tests
    ✓ Outbound federation can query profile data
    × Inbound federation can query profile data

50federation/11query-directory 0/2 tests
50federation/30room-join 0/17 tests
50federation/31room-send 0/5 tests
50federation/32room-getevent 0/2 tests
50federation/33room-get-missing-events 1/4 tests
    × Outbound federation can request missing events
    × Inbound federation can return missing events for $vis visibility
    × outliers whose auth_events are in a different room are correctly rejected
    ✓ Outbound federation will ignore a missing event with bad JSON for room version 6

50federation/34room-backfill 0/5 tests
50federation/35room-invite 0/12 tests
50federation/36state 0/11 tests
50federation/37public-rooms 0/1 tests
50federation/38receipts 0/2 tests
50federation/39redactions 0/4 tests
50federation/40devicelists 0/11 tests
50federation/40publicroomlist 0/1 tests
50federation/41power-levels 0/2 tests
50federation/43typing 0/1 tests
50federation/50no-deextrem-outliers 0/1 tests
50federation/50server-acl-endpoints 0/1 tests
50federation/51transactions 0/2 tests
50federation/52soft-fail 0/3 tests
51media/01unicode 0/5 tests
51media/02nofilename 3/3 tests
    ✓ Can upload without a file name
    ✓ Can download without a file name locally
    ✓ Can download without a file name over federation

51media/03ascii 0/5 tests
51media/10thumbnail 0/2 tests
51media/20urlpreview 0/1 tests
51media/30config 0/1 tests
51media/48admin-quarantine 0/1 tests
52user-directory/01public 0/7 tests
52user-directory/02private 0/3 tests
53groups/01create 0/3 tests
53groups/02read 0/4 tests
53groups/03local 0/3 tests
53groups/04remote-group 0/3 tests
53groups/05categories 0/3 tests
53groups/05roles 0/3 tests
53groups/06summaries 0/11 tests
53groups/10sync 0/3 tests
53groups/11publicise 0/2 tests
53groups/12joinable 0/4 tests
53groups/20room-upgrade 0/1 tests
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
61push/06_push_rules_in_sync 0/5 tests
61push/07_set_enabled 0/2 tests
61push/08_rejected_pushers 0/1 tests
61push/09_notifications_api 0/1 tests
61push/80torture 0/3 tests
80torture/03events 0/5 tests
80torture/10filters 0/1 tests
80torture/20json 0/3 tests
90jira/SYN-115 0/1 tests
90jira/SYN-202 0/1 tests
90jira/SYN-205 0/1 tests
90jira/SYN-328 0/1 tests
90jira/SYN-343 0/1 tests
90jira/SYN-390 0/1 tests
90jira/SYN-442 0/1 tests
90jira/SYN-516 0/1 tests
90jira/SYN-627 0/1 tests

TOTAL: 11/690 tests converted

```
