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
(cd dockerfiles && docker build -t complement-dendrite -f Dendrite.Dockerfile .)
# ...and test it
COMPLEMENT_BASE_IMAGE=complement-dendrite:latest go test -v ./tests
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

#### Why 'Complement'?

Because **M**<sup>*C*</sup> = **1** - **M**

#### Sytest parity

```
$ go run sytest_coverage.go

account_change_password_test.go 0/7 tests
account_data_test.go 0/10 tests
account_deactivate_test.go 0/4 tests
admin_test.go 0/5 tests
apidoc_content_test.go 0/2 tests
apidoc_device_management_test.go 0/8 tests
apidoc_events_initial_test.go 0/2 tests
apidoc_login_test.go 0/6 tests
apidoc_presence_test.go 0/2 tests
apidoc_profile_avatar_url_test.go 0/2 tests
apidoc_profile_displayname_test.go 0/2 tests
apidoc_register_test.go 3/9 tests
    × GET /register yields a set of flows
    ✓ POST /register can create a user
    ✓ POST /register downcases capitals in usernames
    ✓ POST /register returns the same device_id as that in the request
    × POST /register rejects registration of usernames with '$q'
    × POST $ep_name with shared secret
    × POST $ep_name admin with shared secret
    × POST $ep_name with shared secret downcases capitals
    × POST $ep_name with shared secret disallows symbols

apidoc_request_encoding_test.go 0/1 tests
apidoc_room_alias_test.go 0/2 tests
apidoc_room_create_test.go 0/10 tests
apidoc_room_levels_test.go 0/4 tests
apidoc_room_members_test.go 0/8 tests
apidoc_room_messages_test.go 0/5 tests
apidoc_room_read_marker_test.go 0/1 tests
apidoc_room_receipts_test.go 0/1 tests
apidoc_room_state_test.go 0/14 tests
apidoc_room_typing_test.go 0/1 tests
apidoc_server_capabilities_test.go 0/2 tests
apidoc_ui_auth_test.go 0/4 tests
apidoc_version_test.go 0/1 tests
app_services_as_create_test.go 0/7 tests
app_services_asuser_test.go 0/2 tests
app_services_deactivate_test.go 0/1 tests
app_services_ghost_test.go 0/6 tests
app_services_lookuppe_test.go 0/4 tests
app_services_passive_test.go 0/3 tests
app_services_publicroomlist_test.go 0/2 tests
direct_directmessage_test.go 0/3 tests
direct_federation_test.go 0/2 tests
direct_polling_test.go 0/1 tests
direct_reliability_test.go 0/2 tests
direct_wildcard_test.go 0/4 tests
end_to_end_keys__backup_test.go 0/10 tests
end_to_end_keys__cross_signing_test.go 0/8 tests
end_to_end_keys__device_lists_test.go 0/15 tests
end_to_end_keys__one_time_key_federation_test.go 0/1 tests
end_to_end_keys__one_time_keys_test.go 0/1 tests
end_to_end_keys__query_key_federation_test.go 0/1 tests
end_to_end_keys__upload_key_test.go 0/5 tests
federation_devicelists_test.go 0/11 tests
federation_keys_test.go 1/4 tests
    ✓ Federation key API allows unsigned requests for keys
    × Federation key API can act as a notary server via a $method request
    × Key notary server should return an expired key if it can't find any others
    × Key notary server must not overwrite a valid key with a spurious result from the origin server

federation_no_deextrem_outliers_test.go 0/1 tests
federation_power_levels_test.go 0/2 tests
federation_prepare_test.go 0/2 tests
federation_public_rooms_test.go 0/1 tests
federation_publicroomlist_test.go 0/1 tests
federation_query_directory_test.go 0/2 tests
federation_query_profile_test.go 1/2 tests
    ✓ Outbound federation can query profile data
    × Inbound federation can query profile data

federation_receipts_test.go 0/2 tests
federation_redactions_test.go 0/4 tests
federation_room_backfill_test.go 0/5 tests
federation_room_get_missing_events_test.go 1/4 tests
    × Outbound federation can request missing events
    × Inbound federation can return missing events for $vis visibility
    × outliers whose auth_events are in a different room are correctly rejected
    ✓ Outbound federation will ignore a missing event with bad JSON for room version 6

federation_room_getevent_test.go 0/2 tests
federation_room_invite_test.go 0/12 tests
federation_room_join_test.go 0/17 tests
federation_room_send_test.go 0/5 tests
federation_server_acl_endpoints_test.go 0/1 tests
federation_server_names_test.go 0/1 tests
federation_soft_fail_test.go 0/3 tests
federation_state_test.go 0/11 tests
federation_transactions_test.go 0/2 tests
federation_typing_test.go 0/1 tests
groups_categories_test.go 0/3 tests
groups_create_test.go 0/3 tests
groups_joinable_test.go 0/4 tests
groups_local_test.go 0/3 tests
groups_publicise_test.go 0/2 tests
groups_read_test.go 0/4 tests
groups_remote_group_test.go 0/3 tests
groups_roles_test.go 0/3 tests
groups_room_upgrade_test.go 0/1 tests
groups_summaries_test.go 0/11 tests
groups_sync_test.go 0/3 tests
identity_test.go 0/6 tests
ignore_test.go 0/3 tests
jira_SYN__test.go 0/9 tests
login_cas_test.go 0/3 tests
login_threepid_and_password_test.go 0/1 tests
logout_test.go 0/4 tests
media_admin_quarantine_test.go 0/1 tests
media_ascii_test.go 0/5 tests
media_config_test.go 0/1 tests
media_nofilename_test.go 0/3 tests
media_thumbnail_test.go 0/2 tests
media_unicode_test.go 0/5 tests
media_urlpreview_test.go 0/1 tests
openid_test.go 0/3 tests
presence_events_test.go 0/3 tests
presence_test.go 0/5 tests
push__get_pusher_test.go 0/1 tests
push__notifications_api_test.go 0/1 tests
push__push_rules_in_sync_test.go 0/5 tests
push__rejected_pushers_test.go 0/1 tests
push__set_actions_test.go 0/4 tests
push__set_enabled_test.go 0/2 tests
push__unread_count_test.go 0/2 tests
push_add_rules_test.go 0/7 tests
push_message_pushed_test.go 0/7 tests
push_torture_test.go 0/3 tests
register_test.go 0/8 tests
room_versions_test.go 0/6 tests
rooms_aliases_test.go 0/13 tests
rooms_ban_test.go 0/2 tests
rooms_context_test.go 0/4 tests
rooms_erasure_test.go 0/1 tests
rooms_event_test.go 0/3 tests
rooms_eventstream_test.go 0/2 tests
rooms_forget_test.go 0/5 tests
rooms_guestaccess_test.go 0/13 tests
rooms_history_visibility_test.go 0/2 tests
rooms_invite_test.go 0/12 tests
rooms_joinedapis_test.go 0/2 tests
rooms_kick_test.go 0/2 tests
rooms_leaving_test.go 0/7 tests
rooms_levels_test.go 0/3 tests
rooms_members_local_test.go 0/5 tests
rooms_members_remote_test.go 0/8 tests
rooms_members_test.go 0/3 tests
rooms_messages_test.go 0/9 tests
rooms_override_per_room_test.go 0/2 tests
rooms_profile_test.go 0/1 tests
rooms_publicroomslist_test.go 0/5 tests
rooms_receipts_test.go 0/3 tests
rooms_redactions_test.go 0/5 tests
rooms_state_test.go 2/9 tests
    ✓ Room creation reports m.room.create to myself
    ✓ Room creation reports m.room.member to myself
    × Setting room topic reports m.room.topic to myself
    × Global initialSync
    × Global initialSync with limit=0 gives no messages
    × Room initialSync
    × Room initialSync with limit=0 gives no messages
    × Setting state twice is idempotent
    × Joining room twice is idempotent

rooms_thirdpartyinvite_test.go 0/13 tests
rooms_typing_test.go 0/3 tests
rooms_version_upgrade_test.go 0/19 tests
search_test.go 0/5 tests
sync_archived_ban_test.go 0/3 tests
sync_archived_test.go 0/8 tests
sync_filter_test.go 0/2 tests
sync_filtered_sync_test.go 0/2 tests
sync_invited_test.go 0/3 tests
sync_joined_test.go 0/6 tests
sync_lazy_members_test.go 0/11 tests
sync_polling_test.go 0/2 tests
sync_presence_test.go 0/3 tests
sync_read_markers_test.go 0/3 tests
sync_receipts_test.go 0/2 tests
sync_room_summary_test.go 0/4 tests
sync_state_test.go 0/14 tests
sync_sync_test.go 0/1 tests
sync_timeline_test.go 0/8 tests
sync_typing_test.go 0/3 tests
tags_test.go 0/10 tests
torture_events_test.go 0/5 tests
torture_filters_test.go 0/1 tests
torture_json_test.go 0/3 tests
user_directory_private_test.go 0/3 tests
user_directory_public_test.go 0/7 tests

TOTAL: 8/694 tests converted
```