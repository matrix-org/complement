package tests

import (
	"fmt"
	"testing"

	"github.com/matrix-org/complement/internal/b"
)

func TestConcurrency(t *testing.T) {
	var servers []b.Homeserver
	num := 10
	for i := 0; i < num; i++ {
		s := b.Homeserver{
			Name: fmt.Sprintf("hs%d", i),
			Users: []b.User{
				{
					Localpart:   "@alice",
					DisplayName: "Alice",
				},
			},
		}
		servers = append(servers, s)
	}
	d := Deploy(t, b.MustValidate(b.Blueprint{
		Name:        "blargle",
		Homeservers: servers,
	}))
	defer d.Destroy(t)

	for i := 0; i < num; i++ {
		hs := fmt.Sprintf("hs%d", i)
		alice := d.Client(t, hs, "@alice:"+hs)
		roomID := alice.CreateRoom(t, map[string]interface{}{
			"preset": "public_chat",
		})
		t.Logf(roomID)
	}
}
