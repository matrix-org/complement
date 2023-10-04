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

// BlueprintPerfManyRooms contains a homeserver with 2 users, who are joined to hundreds of rooms with a few messages in each.
var BlueprintPerfManyRooms = MustValidate(Blueprint{
	Name: "perf_many_rooms",
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
			Rooms: perfManyRooms([]string{"@alice", "@bob"}, 400),
		},
	},
})

func perfManyRooms(users []string, count int) []Room {
	rooms := make([]Room, count)
	for i := 0; i < count; i++ {
		creator := users[i%len(users)]
		var events []Event
		for j := 0; j < len(users); j++ {
			if users[j] == creator {
				continue
			}
			// join the room
			events = append(events, Event{
				Type:     "m.room.member",
				StateKey: Ptr(users[j]),
				Content: map[string]interface{}{
					"membership": "join",
				},
				Sender: users[j],
			})
		}

		events = append(events, manyMessages(users, 20)...)

		room := Room{
			CreateRoom: map[string]interface{}{
				"preset": "public_chat",
			},
			Creator: creator,
			Events:  events,
		}
		rooms[i] = room
	}
	return rooms
}
