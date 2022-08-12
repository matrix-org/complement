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

var manyUsersList = manyUsers(1000)

func makeEvents(homeserver string) []Event {
	events := []Event{
		{
			Type:     "m.room.member",
			StateKey: Ptr("@bob:hs1"),
			Content: map[string]interface{}{
				"membership": "join",
			},
			Sender: "@bob",
		},
	}
	events = append(events, manJoinEvents(homeserver, manyUsersList)...)
	events = append(events, manyMessages(getSendersFromUsers(manyUsersList), 1400)...)

	//fmt.Printf("events made %d events=%+v", len(events), events)

	return events
}

// BlueprintPerfManyMessages contains a homeserver with 2 users, who are joined to the same room with thousands of messages.
var BlueprintPerfManyMessages = MustValidate(Blueprint{
	Name: "perf_many_messages",
	Homeservers: []Homeserver{
		{
			Name: "hs1",
			Users: append([]User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
				},
				{
					Localpart:   "@bob",
					DisplayName: "Bob",
				},
			}, manyUsersList...),
			Rooms: []Room{
				{
					CreateRoom: map[string]interface{}{
						"preset": "public_chat",
					},
					Creator: "@alice",
					Events:  makeEvents("hs1"),
				},
			},
		},
		{
			Name: "hs2",
			Users: []User{
				{
					Localpart:   "@charlie",
					DisplayName: "Charlie",
				},
			},
		},
	},
})
