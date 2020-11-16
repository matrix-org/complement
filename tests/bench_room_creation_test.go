// +build bench

package tests

import (
	"fmt"
	"sync"
	"testing"

	"github.com/matrix-org/complement/internal/b"
)

func TestParallelRoomCreation(t *testing.T) {
	deployment := Deploy(t, "bench_room_creation", b.BlueprintAlice)
	defer deployment.Destroy(t)

	userID := "@alice:hs1"
	numRooms := 800
	numWorkers := 8
	ch := make(chan string, numRooms)
	for i := 0; i < numRooms; i++ {
		ch <- fmt.Sprintf("room_alias_%d", i)
	}
	close(ch)
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			alice := deployment.Client(t, "hs1", userID)
			for aliasName := range ch {
				alice.CreateRoom(t, map[string]string{
					"visibility":      "public",
					"room_alias_name": aliasName,
				})
				if t.Failed() {
					break
				}
			}
		}()
	}
	wg.Wait()
}
