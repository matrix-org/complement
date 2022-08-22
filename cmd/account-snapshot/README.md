### Account Snapshot

This is capable of taking an anonymised snapshot of a Matrix account and then create a Homerunner in-line blueprint for it. It has several stages:
 - Perform a full `/sync` request. This is cached in `sync_snapshot.json` for subsequent runs in case something fails.
 - Redact the `/sync` response. This removes all PII including user IDs, messages, room IDs, event IDs, attachments, avatars, display names, etc.
   An intermediate `Snapshot` struct is the returned.
   A whitelist approach is used, where only specific event types are persisted. This guarantees that non-spec state events which could be revealing
   are ignored. However, we apply a blacklist approach for spec-defined event types. This means it is possible that non-spec fields may leak through
   into the subsequent stages. A reasonably easy way to check to see how well the redaction process worked is to:
   ```
   grep -Ei "the|matrix|and|mxc" blueprint.json | sort | uniq
   ```
   As this will flag `mxc://` URIs as well as hopefully any textual messages.
 - Map the `Snapshot` to a `Blueprint` which is capable to be run using `./cmd/homerunner`. The `Blueprint` consists of all the users in the `/sync`
   response, all joined rooms, and the most recent 50 messages.


To try it out: (this will take between 5-60 mins depending on how large your account is)
```
./account-snapshot -user @alice:matrix.org -token MDA.... > blueprint.json
```
Then run Homerunner in single-shot mode: (this will take hours or days depending on the homeserver and how many events there are)
```
HOMERUNNER_SNAPSHOT_BLUEPRINT=blueprint.json ./homerunner
```
This will execute the blueprint and commit the resulting images so you can push them to docker hub/gitlab. IMPORTANT: Make sure to set `HOMERUNNER_KEEP_BLUEPRINTS=your-blueprint-name` (TODO: Update) when running homerunner subsequently or **it will clean up the image**.

#### Limitations

Currently, the `/sync` -> snapshot does not:
 - Handle push rules in account data
 - Handle invites
 - Handle third party invites

Currently, the snapshot -> blueprint does not:
 - Handle `m.reaction` or `m.room.redaction` events - the `/sync` response may not include the events being redacted, so we need to guess/make-up an appropriate
   event to react to or redact.
 - Handle `m.room.tombstone` events. This is more a Complement limitation.
 - Handle `m.room.encrypted` events. Whilst Complement does upload OTKs, it needs to have a field to mark "send this event as E2E" in `b.Event`.
