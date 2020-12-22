// Copyright 2020 The Matrix.org Foundation C.I.C.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package b

// BlueprintPerfManyMessages contains a homeserver with 2 users, who are joined to the same room with thousands of messages.
var BlueprintPerfManyMessages = MustValidate(Blueprint{
	Name: "perf_many_messages",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
			Users: []User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
				},
				{
					Localpart:   "@bob",
					DisplayName: "Bob",
				},
			},
			Rooms: []Room{
				{
					CreateRoom: map[string]interface{}{
						"preset": "public_chat",
					},
					Creator: "@alice",
					Events: append([]Event{
						Event{
							Type:     "m.room.member",
							StateKey: Ptr("@bob:hs1"),
							Content: map[string]interface{}{
								"membership": "join",
							},
							Sender: "@bob",
						},
					}, manyMessages([]string{"@alice", "@bob"}, 7000)...),
				},
			},
		},
	},
})
