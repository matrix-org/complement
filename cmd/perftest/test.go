package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/internal/docker"
	"github.com/matrix-org/complement/internal/instruction"
)

func withSpan(spanName, desc string, snapshots []Snapshot, absStartTime time.Time, deployment *docker.Deployment, fn func() error) ([]Snapshot, error) {
	startTime := time.Now()
	if err := fn(); err != nil {
		return nil, fmt.Errorf("withSpan %s: %s", spanName, err)
	}
	duration := time.Since(startTime)
	absDuration := time.Since(absStartTime)
	snapshots = append(snapshots, snapshotStats(spanName, desc, deployment, absDuration, duration)...)
	return snapshots, nil
}

func runTest(testName string, builder *docker.Builder, deployer *docker.Deployer, seed int64) ([]Snapshot, error) {
	randSource := rand.NewSource(seed)
	rnd := rand.New(randSource)
	// deploy a base image
	if err := builder.ConstructBlueprintIfNotExist(b.BlueprintCleanHS); err != nil {
		return nil, err
	}
	deployment, err := deployer.Deploy(context.Background(), b.BlueprintCleanHS)
	if err != nil {
		return nil, err
	}
	defer deployment.Deployer.Destroy(deployment, false, testName, false)

	snapshots := snapshotStats("startup", "clean homeserver with no users", deployment, 0, 0)
	runner := instruction.NewRunner(testName, false, true)

	absStartTime := time.Now()
	numUsers := 10

	snapshots, err = withSpan("create_users", fmt.Sprintf("creates %d users concurrently", numUsers), snapshots, absStartTime, deployment, func() error {
		// make N users
		users := make([]b.User, numUsers)
		for i := 0; i < len(users); i++ {
			users[i] = b.User{
				Localpart:   fmt.Sprintf("user-%d", i),
				DisplayName: fmt.Sprintf("User %d", i),
			}
		}
		return runner.Run(b.Homeserver{
			Name:  "hs1",
			Users: users,
		}, deployment.HS["hs1"].BaseURL)
	})
	if err != nil {
		return nil, err
	}

	var userRoomJoins [][2]string // list of [user_id, roomRef] of joined users who can send messages
	numRooms := 50
	snapshots, err = withSpan("create_rooms", fmt.Sprintf("creates %d public rooms with different users", numRooms), snapshots, absStartTime, deployment, func() error {
		// make M rooms
		rooms := make([]b.Room, numRooms)
		for i := 0; i < numRooms; i++ {
			userI := int(randSource.Int63() % int64(numUsers))
			userID := fmt.Sprintf("@user-%d:hs1", userI)
			roomRef := fmt.Sprintf("ref-%d", i)
			rooms[i] = b.Room{
				Creator: userID,
				CreateRoom: map[string]interface{}{
					"preset": "public_chat",
				},
				Ref: roomRef,
			}
			userRoomJoins = append(userRoomJoins, [2]string{userID, roomRef})
		}
		return runner.Run(b.Homeserver{
			Name:  "hs1",
			Rooms: rooms,
		}, deployment.HS["hs1"].BaseURL)
	})
	if err != nil {
		return nil, err
	}

	// normal distribution around numRooms/2 to join M rooms, P times
	numJoins := 100
	stddev := float64(numRooms) / 6.0
	mean := (float64(numRooms) / 2.0)
	snapshots, err = withSpan("join_rooms", fmt.Sprintf("issues %d /join requests according to a normal distribution (mean=%v,std-dev=%v)", numJoins, mean, stddev), snapshots, absStartTime, deployment, func() error {
		var hs b.Homeserver
		roomMap := make(map[string]b.Room)
		for i := 0; i < numJoins; i++ {
			// random user
			userI := int(randSource.Int63() % int64(numUsers))
			userID := fmt.Sprintf("@user-%d:hs1", userI)
			// normal distribution so we get some large rooms
			roomI := int(rnd.NormFloat64()*stddev + mean)
			ref := fmt.Sprintf("ref-%d", roomI)
			room := roomMap[ref]
			room.Ref = ref
			room.Events = append(room.Events, b.Event{
				Type:     "m.room.member",
				StateKey: &userID,
				Sender:   userID,
				Content: map[string]interface{}{
					"membership": "join",
				},
			})
			roomMap[ref] = room
			userRoomJoins = append(userRoomJoins, [2]string{userID, ref})
		}
		for _, room := range roomMap {
			hs.Rooms = append(hs.Rooms, room)
		}
		return runner.Run(hs, deployment.HS["hs1"].BaseURL)
	})
	if err != nil {
		return nil, err
	}

	numEvents := 100
	snapshots, err = withSpan("send_msgs", fmt.Sprintf("sends %d m.room.message text events into random rooms users have joined", numEvents), snapshots, absStartTime, deployment, func() error {
		var hs b.Homeserver
		roomMap := make(map[string]b.Room)
		for i := 0; i < numEvents; i++ {
			userAndRoom := userRoomJoins[int(randSource.Int63()%int64(len(userRoomJoins)))]
			userID := userAndRoom[0]
			ref := userAndRoom[1]
			room := roomMap[ref]
			room.Ref = ref
			room.Events = append(room.Events, b.Event{
				Sender: userID,
				Type:   "m.room.message",
				Content: map[string]interface{}{
					"body":    fmt.Sprintf("message %d", i),
					"msgtype": "m.text",
				},
			})
			roomMap[ref] = room
		}
		for _, room := range roomMap {
			hs.Rooms = append(hs.Rooms, room)
		}

		return runner.Run(hs, deployment.HS["hs1"].BaseURL)
	})
	if err != nil {
		return nil, err
	}

	runOpts := instruction.RunOpts{
		Concurrency:    instruction.ConcurrencyTypePerUser,
		HSURL:          deployment.HS["hs1"].BaseURL,
		StoreNamespace: "_syncs",
	}
	syncInstructions := make([]instruction.Instr, numUsers)
	for i := range syncInstructions {
		userID := fmt.Sprintf("@user-%d:hs1", i)
		syncInstructions[i] = instruction.Instr{
			UserID:  userID,
			Method:  "GET",
			Path:    "/_matrix/client/v3/sync",
			Queries: map[string]string{"timeout": "0"},
			Store: map[string]string{
				userID: ".next_batch",
			},
		}
	}
	snapshots, err = withSpan("initial_syncs", fmt.Sprintf("performs /sync with no since token and timeout=0 for all users"), snapshots, absStartTime, deployment, func() error {
		return runner.RunInstructions(runOpts, syncInstructions)
	})
	if err != nil {
		return nil, err
	}

	syncInstructions = make([]instruction.Instr, 0, numUsers)
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("@user-%d:hs1", i)
		syncInstructions = append(syncInstructions, instruction.Instr{
			UserID: userID,
			Method: "PUT",
			Path:   fmt.Sprintf("/_matrix/client/v3/profile/%s/displayname", url.PathEscape(userID)),
			Body: map[string]interface{}{
				"displayname": fmt.Sprintf("Updated User %d", i),
			},
		})
	}
	snapshots, err = withSpan("display_name_change", fmt.Sprintf("updates the displayname of all %d users concurrently", numUsers), snapshots, absStartTime, deployment, func() error {
		return runner.RunInstructions(runOpts, syncInstructions)
	})
	if err != nil {
		return nil, err
	}

	syncInstructions = make([]instruction.Instr, numUsers)
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("@user-%d:hs1", i)
		syncInstructions[i] = instruction.Instr{
			UserID:  userID,
			Method:  "GET",
			Path:    "/_matrix/client/v3/sync",
			Queries: map[string]string{"since": runner.GetStoredValue(runOpts, userID)},
			Store: map[string]string{
				userID: ".next_batch",
			},
		}
	}
	snapshots, err = withSpan("incremental_sync", fmt.Sprintf("performs an incremental sync on all %d users with the since token from the initial sync", numUsers), snapshots, absStartTime, deployment, func() error {
		return runner.RunInstructions(runOpts, syncInstructions)
	})
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}
