package client

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"io"

	"github.com/matrix-org/complement/ct"
	"github.com/tidwall/gjson"
)

const (
	SharedSecret = "complement"
)

type LoginOpt func(map[string]interface{})

func WithDeviceID(deviceID string) LoginOpt {
	return func(loginBody map[string]interface{}) {
		loginBody["device_id"] = deviceID
	}
}

// LoginUser will log in to a homeserver and create a new device on an existing user.
func (c *CSAPI) LoginUser(t ct.TestLike, localpart, password string, opts ...LoginOpt) (userID, accessToken, deviceID string) {
	t.Helper()
	reqBody := map[string]interface{}{
		"identifier": map[string]interface{}{
			"type": "m.id.user",
			"user": localpart,
		},
		"password": password,
		"type":     "m.login.password",
	}

	for _, opt := range opts {
		opt(reqBody)
	}

	res := c.MustDo(t, "POST", []string{"_matrix", "client", "v3", "login"}, WithJSONBody(t, reqBody))

	body, err := io.ReadAll(res.Body)
	if err != nil {
		ct.Fatalf(t, "unable to read response body: %v", err)
	}

	userID = GetJSONFieldStr(t, body, "user_id")
	accessToken = GetJSONFieldStr(t, body, "access_token")
	deviceID = GetJSONFieldStr(t, body, "device_id")
	return userID, accessToken, deviceID
}

// LoginUserWithRefreshToken will log in to a homeserver, with refresh token enabled,
// and create a new device on an existing user.
func (c *CSAPI) LoginUserWithRefreshToken(t ct.TestLike, localpart, password string) (userID, accessToken, refreshToken, deviceID string, expiresInMs int64) {
	t.Helper()
	reqBody := map[string]interface{}{
		"identifier": map[string]interface{}{
			"type": "m.id.user",
			"user": localpart,
		},
		"password":      password,
		"type":          "m.login.password",
		"refresh_token": true,
	}
	res := c.MustDo(t, "POST", []string{"_matrix", "client", "v3", "login"}, WithJSONBody(t, reqBody))

	body, err := io.ReadAll(res.Body)
	if err != nil {
		ct.Fatalf(t, "unable to read response body: %v", err)
	}

	userID = GetJSONFieldStr(t, body, "user_id")
	accessToken = GetJSONFieldStr(t, body, "access_token")
	deviceID = GetJSONFieldStr(t, body, "device_id")
	refreshToken = GetJSONFieldStr(t, body, "refresh_token")
	expiresInMs = gjson.GetBytes(body, "expires_in_ms").Int()
	return userID, accessToken, refreshToken, deviceID, expiresInMs
}

// RefreshToken will consume a refresh token and return a new access token and refresh token.
func (c *CSAPI) ConsumeRefreshToken(t ct.TestLike, refreshToken string) (newAccessToken, newRefreshToken string, expiresInMs int64) {
	t.Helper()
	reqBody := map[string]interface{}{
		"refresh_token": refreshToken,
	}
	res := c.MustDo(t, "POST", []string{"_matrix", "client", "v3", "refresh"}, WithJSONBody(t, reqBody))

	body, err := io.ReadAll(res.Body)
	if err != nil {
		ct.Fatalf(t, "unable to read response body: %v", err)
	}

	newAccessToken = GetJSONFieldStr(t, body, "access_token")
	newRefreshToken = GetJSONFieldStr(t, body, "refresh_token")
	expiresInMs = gjson.GetBytes(body, "expires_in_ms").Int()
	return newAccessToken, newRefreshToken, expiresInMs
}

// RegisterUser will register the user with given parameters and
// return user ID, access token and device ID. It fails the test on network error.
func (c *CSAPI) RegisterUser(t ct.TestLike, localpart, password string) (userID, accessToken, deviceID string) {
	t.Helper()
	reqBody := map[string]interface{}{
		"auth": map[string]string{
			"type": "m.login.dummy",
		},
		"username": localpart,
		"password": password,
	}
	res := c.MustDo(t, "POST", []string{"_matrix", "client", "v3", "register"}, WithJSONBody(t, reqBody))

	body, err := io.ReadAll(res.Body)
	if err != nil {
		ct.Fatalf(t, "unable to read response body: %v", err)
	}

	userID = GetJSONFieldStr(t, body, "user_id")
	accessToken = GetJSONFieldStr(t, body, "access_token")
	deviceID = GetJSONFieldStr(t, body, "device_id")
	return userID, accessToken, deviceID
}

// RegisterSharedSecret registers a new account with a shared secret via HMAC
// See https://github.com/matrix-org/synapse/blob/e550ab17adc8dd3c48daf7fedcd09418a73f524b/synapse/_scripts/register_new_matrix_user.py#L40
func (c *CSAPI) RegisterSharedSecret(t ct.TestLike, user, pass string, isAdmin bool) (userID, accessToken, deviceID string) {
	resp := c.Do(t, "GET", []string{"_synapse", "admin", "v1", "register"})
	if resp.StatusCode != 200 {
		t.Skipf("Homeserver image does not support shared secret registration, /_synapse/admin/v1/register returned HTTP %d", resp.StatusCode)
		return
	}
	body := ParseJSON(t, resp)
	nonce := gjson.GetBytes(body, "nonce")
	if !nonce.Exists() {
		ct.Fatalf(t, "Malformed shared secret GET response: %s", string(body))
	}
	mac := hmac.New(sha1.New, []byte(SharedSecret))
	mac.Write([]byte(nonce.Str))
	mac.Write([]byte("\x00"))
	mac.Write([]byte(user))
	mac.Write([]byte("\x00"))
	mac.Write([]byte(pass))
	mac.Write([]byte("\x00"))
	if isAdmin {
		mac.Write([]byte("admin"))
	} else {
		mac.Write([]byte("notadmin"))
	}
	sig := mac.Sum(nil)
	reqBody := map[string]interface{}{
		"nonce":    nonce.Str,
		"username": user,
		"password": pass,
		"mac":      hex.EncodeToString(sig),
		"admin":    isAdmin,
	}
	resp = c.MustDo(t, "POST", []string{"_synapse", "admin", "v1", "register"}, WithJSONBody(t, reqBody))
	body = ParseJSON(t, resp)
	userID = GetJSONFieldStr(t, body, "user_id")
	accessToken = GetJSONFieldStr(t, body, "access_token")
	deviceID = GetJSONFieldStr(t, body, "device_id")
	return userID, accessToken, deviceID
}
