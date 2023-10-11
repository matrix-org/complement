package csapi_tests

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/b"
)

func TestAvatarUrlUpdate(t *testing.T) {
	testProfileFieldUpdate(t, "avatar_url")
}

func TestDisplayNameUpdate(t *testing.T) {
	testProfileFieldUpdate(t, "displayname")
}

// sytest: $datum updates affect room member events
func testProfileFieldUpdate(t *testing.T, field string) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	const bogusData = "LemurLover"

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	roomID := alice.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat",
	})

	sinceToken := alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(alice.UserID, roomID))

	alice.MustDo(
		t,
		"PUT",
		[]string{"_matrix", "client", "v3", "profile", alice.UserID, field},
		client.WithJSONBody(t, map[string]interface{}{
			field: bogusData,
		}),
	)

	alice.MustSyncUntil(t, client.SyncReq{Since: sinceToken}, client.SyncJoinedTo(alice.UserID, roomID, func(result gjson.Result) bool {
		return result.Get("content."+field).Str == bogusData
	}))
}
