package tests

//
//import (
//	"testing"
//
//	"encoding/json"
//
//	"github.com/matrix-org/complement/internal/b"
//	"github.com/matrix-org/complement/internal/client"
//	"github.com/matrix-org/complement/internal/match"
//	"github.com/matrix-org/complement/internal/must"
//)
//
//func TestChangePassword(t *testing.T) {
//	deployment := Deploy(t, b.BlueprintAlice)
//	defer deployment.Destroy(t)
//	unauthedClient := deployment.Client(t, "hs1", "")
//	newPassword := "new_superuser"
//	// sytest: After changing password, can't log in with old password
//	t.Run("After changing password, can't log in with old password", func(t *testing.T) {
//		CreateDummyUser(t, unauthedClient, "cant_login_with_old_password")
//		unauthedClient1 := deployment.Client(t, "hs1", "@cant_login_with_old_password:hs1")
//		res := unauthedClient1.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "account", "password"}, client.WithRawBody(json.RawMessage(`{
//				"auth": {
//					"type": "m.login.password",
//					"user": "@cant_login_with_old_password:hs1",
//					"password": "superuser"
//				},
//				"new_password": "`+newPassword+`"
//			}`)))
//
//		must.MatchResponse(t, res, match.HTTPResponse{
//			StatusCode: 200,
//		})
//
//		res = unauthedClient.DoFunc(t, "POST", []string{"_matrix", "client", "r0", "login"}, client.WithRawBody(json.RawMessage(`{
//				"auth": {
//					"type": "m.login.password",
//					"user": "@cant_login_with_old_password:hs1",
//					"password": "superuser"
//				}
//			}`)))
//		must.MatchResponse(t, res, match.HTTPResponse{
//			StatusCode: 400,
//		})
//	})
//}
