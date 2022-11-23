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

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var (
	// HostnameRunningComplement is the hostname of Complement from the perspective of a Homeserver.
	HostnameRunningComplement = "host.docker.internal"
)

// KnownBlueprints lists static blueprints
var KnownBlueprints = map[string]*Blueprint{
	BlueprintCleanHS.Name:                     &BlueprintCleanHS,
	BlueprintAlice.Name:                       &BlueprintAlice,
	BlueprintFederationOneToOneRoom.Name:      &BlueprintFederationOneToOneRoom,
	BlueprintFederationTwoLocalOneRemote.Name: &BlueprintFederationTwoLocalOneRemote,
	BlueprintHSWithApplicationService.Name:    &BlueprintHSWithApplicationService,
	BlueprintOneToOneRoom.Name:                &BlueprintOneToOneRoom,
	BlueprintPerfManyMessages.Name:            &BlueprintPerfManyMessages,
	BlueprintPerfManyRooms.Name:               &BlueprintPerfManyRooms,
	BlueprintPerfE2EERoom.Name:                &BlueprintPerfE2EERoom,
}

// Blueprint represents an entire deployment to make.
type Blueprint struct {
	// The name of the blueprint. Containers will use this name.
	Name string
	// The list of homeservers to create for this deployment.
	Homeservers []Homeserver
	// A set of user IDs to retain access_tokens for. If empty, all tokens are kept.
	KeepAccessTokensForUsers []string
}

type Homeserver struct {
	// The name of this homeserver. Containers will use this name.
	Name string
	// The list of users to create on this homeserver.
	Users []User
	// The list of rooms to create on this homeserver
	Rooms []Room
	// The list of application services to create on the homeserver
	ApplicationServices []ApplicationService
	// Optionally override the baseImageURI for blueprint creation
	BaseImageURI *string
}

type User struct {
	Localpart   string
	DisplayName string
	AvatarURL   string
	AccountData []AccountData
	DeviceID    *string
	// Enable end-to end encryption for this user and upload the given
	// amount of one-time keys. This requires the DeviceId to be set as
	// well.
	OneTimeKeys uint
}

type AccountData struct {
	Type  string
	Value map[string]interface{}
}

type Room struct {
	// The unique reference for this room. Used to link together rooms across homeservers.
	Ref        string
	Creator    string
	CreateRoom map[string]interface{}
	Events     []Event
}

type ApplicationService struct {
	ID              string
	HSToken         string
	ASToken         string
	URL             string
	SenderLocalpart string
	RateLimited     bool
}

type Event struct {
	Type     string
	Sender   string
	StateKey *string
	Content  map[string]interface{}

	/* The following fields are ignored in blueprints as clients are unable to set them.
	 * They are used with federation.Server.
	 */

	Unsigned map[string]interface{}

	// The events needed to authenticate this event.
	// This can be either []EventReference for room v1/v2, or []string for room v3 onwards.
	// If it is left at nil, MustCreateEvent will populate it automatically based on the room state.
	AuthEvents interface{}

	// The prev events of the event if we want to override or falsify them.
	// If it is left at nil, MustCreateEvent will populate it automatically based on the forward extremities.
	PrevEvents interface{}

	// If this is a redaction, the event that it redacts
	Redacts string
}

func MustValidate(bp Blueprint) Blueprint {
	bp2, err := Validate(bp)
	if err != nil {
		panic("MustValidate: " + err.Error())
	}
	return bp2
}

func Validate(bp Blueprint) (Blueprint, error) {
	if bp.Name == "" {
		return bp, fmt.Errorf("Blueprint must have a Name")
	}
	var err error
	for _, hs := range bp.Homeservers {
		for i, u := range hs.Users {
			if !strings.HasPrefix(u.Localpart, "@") {
				return bp, fmt.Errorf("HS %s user localpart '%s' must start with '@'", hs.Name, u.Localpart)
			}
			if strings.Contains(u.Localpart, ":") {
				return bp, fmt.Errorf("HS %s user localpart '%s' must not contain a domain", hs.Name, u.Localpart)
			}
			// strip the @
			hs.Users[i].Localpart = hs.Users[i].Localpart[1:]
		}
		for i := range hs.Rooms {
			hs.Rooms[i], err = normaliseRoom(hs.Name, hs.Rooms[i])
			if err != nil {
				return bp, err
			}
		}
		for i, as := range hs.ApplicationServices {
			hs.ApplicationServices[i], err = normalizeApplicationService(as)
			if err != nil {
				return bp, err
			}
		}
	}

	return bp, nil
}

func normaliseRoom(hsName string, r Room) (Room, error) {
	var err error
	if r.Creator != "" {
		r.Creator, err = normaliseUser(r.Creator, hsName)
		if err != nil {
			return r, err
		}
	} else if r.Ref == "" {
		return r, fmt.Errorf("%s : room must have either a Ref or a Creator", hsName)
	}
	for i := range r.Events {
		r.Events[i].Sender, err = normaliseUser(r.Events[i].Sender, hsName)
		if err != nil {
			return r, err
		}
		if r.Events[i].StateKey != nil && r.Events[i].Type == "m.room.member" {
			skey, err := normaliseUser(*r.Events[i].StateKey, hsName)
			if err != nil {
				return r, err
			}
			r.Events[i].StateKey = &skey
		}
	}
	return r, nil
}

func normaliseUser(u string, hsName string) (string, error) {
	// if they did it as @foo:bar make sure :bar is the name of the HS
	if strings.Contains(u, ":") {
		if strings.HasSuffix(u, fmt.Sprintf(":%s", hsName)) {
			return u, nil
		}
		return "", fmt.Errorf("HS '%s' user '%s' must end with ':%s' or have no domain", hsName, u, hsName)
	}
	// add :domain
	if !strings.Contains(u, ":") {
		u += ":" + hsName
	}
	return u, nil
}

// Asks the kernel for a free open port that is ready to use.
// via https://github.com/phayes/freeport/blob/95f893ade6f232a5f1511d61735d89b1ae2df543/freeport.go#L7-L20
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func normalizeApplicationService(as ApplicationService) (ApplicationService, error) {
	hsToken := make([]byte, 32)
	_, err := rand.Read(hsToken)
	if err != nil {
		return as, err
	}

	asToken := make([]byte, 32)
	_, err = rand.Read(asToken)
	if err != nil {
		return as, err
	}

	as.HSToken = hex.EncodeToString(hsToken)
	as.ASToken = hex.EncodeToString(asToken)

	if as.URL == "" {
		// Since, we're just checking and not reserving the port, we could
		// potentially run into an issue where the port is no longer available when
		// we actually try to bind to it later on
		var port int
		port, err = getFreePort()
		if err != nil {
			return as, err
		}

		as.URL = fmt.Sprintf("http://%s:%d", HostnameRunningComplement, port)
	}

	return as, err
}

// Ptr returns a pointer to `in`, because Go doesn't allow you to inline this.
func Ptr(in string) *string {
	return &in
}

func manyMessages(senders []string, count int) []Event {
	evs := make([]Event, count)
	for i := 0; i < len(evs); i++ {
		sender := senders[i%len(senders)]
		evs[i] = Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"body":    "Hello world " + strconv.Itoa(i),
				"msgtype": "m.text",
			},
			Sender: sender,
		}
	}
	return evs
}
