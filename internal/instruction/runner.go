package instruction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/crypto/olm"

	"github.com/matrix-org/complement/internal/b"
)

type Runner struct {
	blueprintName string
	lookup        *sync.Map // string -> string
	debugLogging  bool
	concurrency   int
}

func NewRunner(blueprintName string, debugLogging bool) *Runner {
	return &Runner{
		lookup:        &sync.Map{},
		blueprintName: blueprintName,
		debugLogging:  debugLogging,
		concurrency:   8, // number of in-flight HTTP requests at any one time
	}
}

func (r *Runner) log(str string, args ...interface{}) {
	if !r.debugLogging {
		return
	}
	log.Printf(str, args...)
}

// AccessTokens returns the access tokens for all users who were created on the given HS domain.
// Returns a map of user_id => access_token
func (r *Runner) AccessTokens(hsDomain string) map[string]string {
	res := make(map[string]string)
	r.lookup.Range(func(k, v interface{}) bool {
		key := k.(string)
		val := v.(string)
		if strings.HasPrefix(key, "user_@") && strings.HasSuffix(key, ":"+hsDomain) {
			res[strings.TrimPrefix(key, "user_")] = val
		}
		return true
	})
	return res
}

// Run all instructions until completion. Return an error if there was a problem executing any instruction.
func (r *Runner) Run(hs b.Homeserver, hsURL string) (resErr error) {
	userInstrSets := calculateUserInstructionSets(r, hs)
	var wg sync.WaitGroup
	wg.Add(len(userInstrSets))
	for _, set := range userInstrSets {
		go func(s []instruction) {
			defer wg.Done()
			err := r.runInstructionSet(hs, hsURL, s)
			if err != nil {
				resErr = err
			}
		}(set)
	}
	// wait for all users to be made before creating rooms
	wg.Wait()
	if resErr != nil {
		r.log("Terminating: user creation failed: %s", resErr)
		return resErr
	}
	roomInstrSets := calculateRoomInstructionSets(r, hs)
	wg.Add(len(roomInstrSets))
	for _, set := range roomInstrSets {
		go func(s []instruction) {
			defer wg.Done()
			err := r.runInstructionSet(hs, hsURL, s)
			if err != nil {
				resErr = err
			}
		}(set)
	}
	// wait for all rooms to be made before returning from this function
	wg.Wait()
	return resErr
}

func (r *Runner) runInstructionSet(hs b.Homeserver, hsURL string, instrs []instruction) error {
	contextStr := fmt.Sprintf("%s.%s", r.blueprintName, hs.Name)
	i := 0
	cli := http.Client{
		Timeout: 10 * time.Second,
	}
	req, instr, i := r.next(instrs, hsURL, i)
	for req != nil {
		res, err := cli.Do(req)
		if err != nil {
			return fmt.Errorf("%s : failed to perform HTTP request to %s: %w", contextStr, req.URL.String(), err)
		}
		r.log("%s [%d/%d] %s => HTTP %s\n", contextStr, i+1, len(instrs), req.URL.String(), res.Status)
		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			return fmt.Errorf("%s : failed to read response body: %w", contextStr, err)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			r.log("LOOKUP : %+v\n", r.lookup)
			r.log("INSTRUCTION: %+v\n", instr)
			return fmt.Errorf("%s : request %s returned HTTP %s : %s", contextStr, req.URL.String(), res.Status, string(body))
		}
		if instr.storeResponse != nil {
			if !gjson.ValidBytes(body) {
				return fmt.Errorf("%s : cannot storeResponse as response is not valid JSON. Body: %s", contextStr, string(body))
			}
			for k, v := range instr.storeResponse {
				val := gjson.GetBytes(body, strings.TrimPrefix(v, "."))
				r.lookup.Store(k, val.Str)
			}
		}
		req, instr, i = r.next(instrs, hsURL, i)
	}
	return nil
}

// next returns the next request to make, along with its instruction. Return nil if there are no more instructions.
func (r *Runner) next(instrs []instruction, hsURL string, i int) (*http.Request, *instruction, int) {
	if i >= len(instrs) {
		return nil, nil, 0
	}
	instr := instrs[i]
	i++

	var body io.Reader
	if instr.body != nil {
		b, err := json.Marshal(instr.body)
		if err != nil {
			r.log("Stopping. Failed to marshal JSON request for instruction: %s -- %+v", err, instr)
			return nil, nil, 0
		}
		body = bytes.NewBuffer(b)
	}
	req, err := http.NewRequest(instr.method, instr.url(hsURL, r.lookup), body)
	if err != nil {
		r.log("Stopping. Failed to form NewRequest for instruction: %s -- %+v \n", err, instr)
		return nil, nil, 0
	}

	q := req.URL.Query()
	if instr.accessToken != "" {
		at, ok := r.lookup.Load(instr.accessToken)
		if ok {
			q.Set("access_token", at.(string))
		}
	}
	for paramName, paramValue := range instr.queryParams {
		// if the variable starts with a '.' then use the lookup table, else use the string literally
		// this handles scenarios like:
		// { $roomId: ".room_0", $eventType: "m.room.message" }
		var valToEncode string
		if paramValue[0] == '.' {
			vint, ok := r.lookup.Load(strings.TrimPrefix(paramValue, "."))
			if ok {
				valToEncode = vint.(string)
			}
		} else {
			valToEncode = paramValue
		}
		q.Set(paramName, valToEncode)
	}
	req.URL.RawQuery = q.Encode()
	return req, &instr, i
}

// instruction represents an HTTP request which should be made to a remote server
type instruction struct {
	// The HTTP method e.g GET POST PUT
	method string
	// The HTTP path, starting with '/', without the base URL. Will have placeholder values of the form $foo which need
	// to be replaced based on 'substitutions'.
	path string
	// Any HTTP query parameters to use in the request
	queryParams map[string]string
	// The HTTP body which will be JSON.Marshal'd
	body interface{}
	// The access_token to use in the request, represented as a key to use in the lookup table e.g "user_@alice:localhost"
	// Empty if no token should be used (e.g /register requests).
	accessToken string
	// The path or query placeholders to replace e.g "/foo/$roomId" with the substitution { $roomId: ".room_1"}.
	// The key is the path param e.g $foo and the value is the lookup table key e.g ".room_id". If the value does not
	// start with a '.' it is interpreted as a literal string to be substituted. e.g { $eventType: "m.room.message" }
	substitutions map[string]string
	// The fields (expressed as dot-style notation) which should be stored in a lookup table for later use.
	// E.g to store the room_id in the response under the key 'foo' to use it later: { "foo" : ".room_id" }
	storeResponse map[string]string
}

// url returns the complete path resolved url for this instruction. Query parameters must be
// added separately.
func (i *instruction) url(hsURL string, lookup *sync.Map) string {
	pathTemplate := i.path
	for k, v := range i.substitutions {
		// if the variable starts with a '.' then use the lookup table, else use the string literally
		// this handles scenarios like:
		// { $roomId: ".room_0", $eventType: "m.room.message" }
		var valToEncode string
		if v != "" && v[0] == '.' {
			vint, ok := lookup.Load(strings.TrimPrefix(v, "."))
			if ok {
				valToEncode = vint.(string)
			}
		} else {
			valToEncode = v
		}
		pathTemplate = strings.Replace(pathTemplate, k, url.PathEscape(valToEncode), -1)
	}
	return hsURL + pathTemplate
}

// calculateUserInstructionSets returns sets of HTTP requests to be executed in order. Sets can be executed in any order.
func calculateUserInstructionSets(r *Runner, hs b.Homeserver) [][]instruction {
	sets := make([][]instruction, r.concurrency)

	createdUsers := make(map[string]bool)
	// add instructions to create users
	for _, user := range hs.Users {
		i := indexFor(user.Localpart, r.concurrency)
		instrs := sets[i]

		if createdUsers[user.Localpart] {
			// login instead as the device ID may be different
			instrs = append(instrs, instructionLogin(hs, user))
		} else {
			instrs = append(instrs, instructionRegister(hs, user))
		}
		createdUsers[user.Localpart] = true

		if user.OneTimeKeys > 0 {
			instrs = append(instrs, instructionOneTimeKeyUpload(hs, user))
		}
		sets[i] = instrs
	}
	return sets
}

// calculateRoomInstructionSets returns sets of HTTP requests to be executed in order. Sets can be executed in any order. Various substitutions
// and placeholders are returned in these instructions as it's impossible to know at this time what room IDs etc
// will be allocated, so use an instruction loader to load the right requests.
func calculateRoomInstructionSets(r *Runner, hs b.Homeserver) [][]instruction {
	sets := make([][]instruction, r.concurrency)

	// add instructions to create rooms and send events
	for roomIndex, room := range hs.Rooms {
		setIndex := indexFor(fmt.Sprintf("%d", roomIndex), r.concurrency)
		instrs := sets[setIndex]
		var queryParams = make(map[string]string)
		if room.Creator != "" {
			storeRes := map[string]string{
				fmt.Sprintf("room_%d", roomIndex): ".room_id",
			}
			if room.Ref != "" {
				storeRes[fmt.Sprintf("room_ref_%s", room.Ref)] = ".room_id"

				// Store the homeserver's server_name, so that remote homeservers that attempt
				// to join this room know which server to contact
				r.lookup.Store(fmt.Sprintf("room_ref_%s_server_name", room.Ref), hs.Name)
			}
			instrs = append(instrs, instruction{
				method:        "POST",
				path:          "/_matrix/client/r0/createRoom",
				accessToken:   "user_" + room.Creator,
				body:          room.CreateRoom,
				storeResponse: storeRes,
			})
		} else if room.Ref == "" {
			log.Printf("HS %s room index %d must either have a Ref or a Creator\n", hs.Name, roomIndex)
			return nil
		}
		for eventIndex, event := range room.Events {
			method := "PUT"
			var path string
			subs := map[string]string{
				"$roomId":    fmt.Sprintf(".room_%d", roomIndex),
				"$eventType": event.Type,
			}
			if room.Ref != "" {
				subs["$roomId"] = fmt.Sprintf(".room_ref_%s", room.Ref)
			}
			if event.StateKey != nil {
				path = "/_matrix/client/r0/rooms/$roomId/state/$eventType/$stateKey"
				subs["$stateKey"] = *event.StateKey
			} else {
				path = "/_matrix/client/r0/rooms/$roomId/send/$eventType/$txnId"
				subs["$txnId"] = fmt.Sprintf("%d", eventIndex)
			}

			// special cases: room joining
			if event.Type == "m.room.member" && event.StateKey != nil &&
				event.Content != nil && event.Content["membership"] != nil {
				membership := event.Content["membership"].(string)
				if membership == "join" {
					path = "/_matrix/client/r0/join/$roomId"
					method = "POST"

					// Set server_name to the homeserver that created the room, as they're a pretty
					// good candidate to join the room through
					queryParams["server_name"] = fmt.Sprintf(".room_ref_%s_server_name", room.Ref)
				}
			}
			instrs = append(instrs, instruction{
				method:        method,
				path:          path,
				body:          event.Content,
				accessToken:   fmt.Sprintf("user_%s", event.Sender),
				substitutions: subs,
				queryParams:   queryParams,
			})
		}
		sets[setIndex] = instrs
	}

	return sets
}

func instructionRegister(hs b.Homeserver, user b.User) instruction {
	body := map[string]interface{}{
		"username": user.Localpart,
		"password": "complement_meets_min_pasword_req_" + user.Localpart,
		"auth": map[string]string{
			"type": "m.login.dummy",
		},
	}

	if user.DeviceID != nil {
		body["device_id"] = user.DeviceID
	}

	return instruction{
		method:      "POST",
		path:        "/_matrix/client/r0/register",
		accessToken: "",
		body:        body,
		storeResponse: map[string]string{
			"user_@" + user.Localpart + ":" + hs.Name: ".access_token",
		},
	}
}

func instructionLogin(hs b.Homeserver, user b.User) instruction {
	body := map[string]interface{}{
		"user":     user.Localpart,
		"password": "complement_meets_min_pasword_req_" + user.Localpart,
		"auth": map[string]string{
			"type": "m.login.dummy",
		},
	}

	if user.DeviceID != nil {
		body["device_id"] = user.DeviceID
	}

	return instruction{
		method:      "POST",
		path:        "/_matrix/client/r0/login",
		accessToken: "",
		body:        body,
		storeResponse: map[string]string{
			"user_@" + user.Localpart + ":" + hs.Name: ".access_token",
		},
	}
}

func instructionOneTimeKeyUpload(hs b.Homeserver, user b.User) instruction {
	account := olm.NewAccount()
	ed25519Key, curveKey := account.IdentityKeys()

	userID := fmt.Sprintf("@%s:%s", user.Localpart, hs.Name)
	deviceID := *user.DeviceID

	ed25519KeyID := fmt.Sprintf("ed25519:%s", deviceID)
	curveKeyID := fmt.Sprintf("curve25519:%s", deviceID)

	deviceKeys := map[string]interface{}{
		"user_id":    userID,
		"device_id":  deviceID,
		"algorithms": []string{"m.olm.v1.curve25519-aes-sha2", "m.megolm.v1.aes-sha2"},
		"keys": map[string]string{
			ed25519KeyID: ed25519Key.String(),
			curveKeyID:   curveKey.String(),
		},
	}

	signature, _ := account.SignJSON(deviceKeys)

	deviceKeys["signatures"] = map[string]map[string]string{
		userID: {
			ed25519KeyID: signature,
		},
	}

	account.GenOneTimeKeys(user.OneTimeKeys)

	oneTimeKeys := map[string]interface{}{}

	for kid, key := range account.OneTimeKeys() {
		keyID := fmt.Sprintf("signed_curve25519:%s", kid)
		keyMap := map[string]interface{}{
			"key": key.String(),
		}

		signature, _ = account.SignJSON(keyMap)

		keyMap["signatures"] = map[string]interface{}{
			userID: map[string]string{
				ed25519KeyID: signature,
			},
		}

		oneTimeKeys[keyID] = keyMap
	}
	return instruction{
		method:      "POST",
		path:        "/_matrix/client/r0/keys/upload",
		accessToken: fmt.Sprintf("user_@%s:%s", user.Localpart, hs.Name),
		body: map[string]interface{}{
			"device_keys":   deviceKeys,
			"one_time_keys": oneTimeKeys,
		},
	}
}

// indexFor hashes the input and returns a number % numEntries
func indexFor(input string, numEntries int) int {
	hh := fnv.New32a()
	_, _ = hh.Write([]byte(input))
	return int(int64(hh.Sum32()) % int64(numEntries))
}
