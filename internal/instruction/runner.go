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
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/crypto/olm"

	"github.com/matrix-org/complement/internal/b"
)

// An instruction for the runner to run.
type Instr struct {
	UserID  string
	Method  string
	Path    string
	Queries map[string]string
	Body    interface{}
	Store   map[string]string
}

type ConcurrencyType int

const (
	// No concurrency: instructions execute in serial.
	ConcurrencyTypeNone ConcurrencyType = iota
	// Per-user concurrency: User requests execute in serial but multiple users can have concurrent requests.
	ConcurrencyTypePerUser
	// All concurrency: All requests are executed at the same time.
	ConcurrencyTypeAll
)

type RunOpts struct {
	Concurrency    ConcurrencyType
	HSURL          string
	StoreNamespace string
}

type Runner struct {
	blueprintName string
	lookup        *sync.Map // string -> string
	debugLogging  bool
	// number of in-flight HTTP requests at any one time
	// this is cpu bounded due to bcrypting passwords
	userConcurrency int
	// number of in-flight HTTP requests at any one time
	// this is not cpu bounded so can be higher than userConcurrency
	roomConcurrency int
	// if true, does not treat non 2xx as fatal
	bestEffort bool
	// set to true if the runner should stop
	terminate atomic.Value
}

func NewRunner(blueprintName string, bestEffort, debugLogging bool) *Runner {
	var v atomic.Value
	v.Store(false)
	return &Runner{
		lookup:          &sync.Map{},
		blueprintName:   blueprintName,
		debugLogging:    debugLogging,
		userConcurrency: 12,
		roomConcurrency: 40,
		terminate:       v,
		bestEffort:      bestEffort,
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

// DeviceIDs returns the device ids for all users who were created on the given HS domain.
// Returns a map of user_id => device_id
func (r *Runner) DeviceIDs(hsDomain string) map[string]string {
	res := make(map[string]string)
	r.lookup.Range(func(k, v interface{}) bool {
		key := k.(string)
		val := v.(string)
		if strings.HasPrefix(key, "device_@") && strings.HasSuffix(key, ":"+hsDomain) {
			res[strings.TrimPrefix(key, "device_")] = val
		}
		return true
	})
	return res
}

// Load a previously stored value from RunInstructions
func (r *Runner) GetStoredValue(opts RunOpts, key string) string {
	fullKey := opts.StoreNamespace + key
	val, ok := r.lookup.Load(fullKey)
	if !ok {
		return ""
	}
	return val.(string)
}

// RunInstructions runs custom instruction sets on this runner.
func (r *Runner) RunInstructions(opts RunOpts, instrs []Instr) (resErr error) {
	sets := r.createInstructionSets(opts, instrs)
	var wg sync.WaitGroup
	wg.Add(len(sets))
	for _, set := range sets {
		go func(s []instruction) {
			defer wg.Done()
			err := r.runInstructionSet("RunInstructions", opts.HSURL, s)
			if err != nil {
				r.log("RunInstructions set failed: %s", err)
				resErr = err
				r.terminate.Store(true)
			}
		}(set)
	}
	wg.Wait()
	if resErr != nil {
		r.log("Terminating: user creation failed: %s", resErr)
		return resErr
	}
	return nil
}

// Run all instructions until completion. Return an error if there was a problem executing any instruction.
func (r *Runner) Run(hs b.Homeserver, hsURL string) (resErr error) {
	userInstrSets := calculateUserInstructionSets(r, hs)
	var wg sync.WaitGroup
	wg.Add(len(userInstrSets))
	for _, set := range userInstrSets {
		go func(s []instruction) {
			defer wg.Done()
			err := r.runInstructionSet(fmt.Sprintf("%s.%s", r.blueprintName, hs.Name), hsURL, s)
			if err != nil {
				r.log("Instruction set failed: %s", err)
				resErr = err
				r.terminate.Store(true)
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
			err := r.runInstructionSet(fmt.Sprintf("%s.%s", r.blueprintName, hs.Name), hsURL, s)
			if err != nil {
				r.log("Instruction set failed: %s", err)
				resErr = err
				r.terminate.Store(true)
			}
		}(set)
	}
	// wait for all rooms to be made before returning from this function
	wg.Wait()
	return resErr
}

func (r *Runner) runInstructionSet(contextStr string, hsURL string, instrs []instruction) error {
	i := 0
	cli := http.Client{
		Timeout: 30 * time.Second,
	}
	isFatalErr := func(err error) error {
		if r.bestEffort {
			r.log("ERROR [bestEffort=true]: %s", err)
			return nil
		}
		return err
	}
	req, instr, i := r.next(instrs, hsURL, i)
	for req != nil {
		if r.terminate.Load().(bool) {
			return fmt.Errorf("terminated")
		}
		res, err := cli.Do(req)
		if err != nil {
			err = isFatalErr(fmt.Errorf("%s : failed to perform HTTP request to %s with body %+v: %w", contextStr, req.URL.String(), instr.body, err))
			if err != nil {
				return err
			}
		}
		// parse the response if we have one (if bestEffort=true then we don't return an error above)
		if res != nil && res.Body != nil {
			if i < 100 || i%200 == 0 {
				r.log("%s [%d/%d] %s => HTTP %s\n", contextStr, i, len(instrs), req.URL.String(), res.Status)
			}
			body, err := ioutil.ReadAll(res.Body)
			if err != nil {
				err = isFatalErr(fmt.Errorf("%s : failed to read response body: %w", contextStr, err))
				if err != nil {
					return err
				}
			}
			if res.StatusCode < 200 || res.StatusCode >= 300 {
				r.log("INSTRUCTION: %+v\n", instr)
				err = isFatalErr(fmt.Errorf("%s : request %s returned HTTP %s : %s", contextStr, req.URL.String(), res.Status, string(body)))
				if err != nil {
					return err
				}
			}
			if instr.storeResponse != nil {
				if !gjson.ValidBytes(body) {
					err = isFatalErr(fmt.Errorf("%s : cannot storeResponse as response is not valid JSON. Body: %s", contextStr, string(body)))
					if err != nil {
						return err
					}
				}
				for k, v := range instr.storeResponse {
					val := gjson.GetBytes(body, strings.TrimPrefix(v, "."))
					r.lookup.Store(k, val.Str)
				}
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
	if instr.body == nil && instr.bodyFn != nil {
		instr.body = instr.bodyFn(r.lookup)
	}
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

func (r *Runner) createInstructionSets(opts RunOpts, instrs []Instr) (sets [][]instruction) {
	switch opts.Concurrency {
	case ConcurrencyTypeAll: // every instruction is its own set
		for _, in := range instrs {
			sets = append(sets, []instruction{
				r.toInstruction(opts, in),
			})
		}
	case ConcurrencyTypeNone: // all instructions in one set
		set := make([]instruction, len(instrs))
		for i, in := range instrs {
			set[i] = r.toInstruction(opts, in)
		}
		sets = append(sets, set)
	case ConcurrencyTypePerUser: // set per user based on user concurrency
		sets = make([][]instruction, r.userConcurrency)
		for _, in := range instrs {
			i := indexFor(in.UserID, r.userConcurrency)
			instrs := sets[i]
			instrs = append(instrs, r.toInstruction(opts, in))
			sets[i] = instrs
		}
	default:
		panic("unknown RunOpts.Concurrency")
	}
	return
}

func (r *Runner) toInstruction(opts RunOpts, i Instr) instruction {
	sr := make(map[string]string)
	for k, v := range i.Store {
		sr[opts.StoreNamespace+k] = v
	}
	return instruction{
		method:        i.Method,
		path:          i.Path,
		queryParams:   i.Queries,
		accessToken:   "user_" + i.UserID,
		body:          i.Body,
		storeResponse: sr,
	}
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
	// Optional: A function to create the request body from the lookup map provided. Only used if `body` is <nil>.
	bodyFn func(lk *sync.Map) interface{}
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
	sets := make([][]instruction, r.userConcurrency)

	createdUsers := make(map[string]bool)
	// add instructions to create users
	for _, user := range hs.Users {
		i := indexFor(user.Localpart, r.userConcurrency)
		instrs := sets[i]

		if createdUsers[user.Localpart] {
			// login instead as the device ID may be different
			instrs = append(instrs, instructionLogin(hs, user))
		} else {
			instrs = append(instrs, instructionRegister(hs, user))
			if user.DisplayName != "" {
				instrs = append(instrs, instructionDisplayName(hs, user))
			}
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
	sets := make([][]instruction, r.roomConcurrency)

	// add instructions to create rooms and send events
	for roomIndex, room := range hs.Rooms {
		setIndex := indexFor(fmt.Sprintf("%d", roomIndex), r.roomConcurrency)
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
				path:          "/_matrix/client/v3/createRoom",
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
				path = "/_matrix/client/v3/rooms/$roomId/state/$eventType/$stateKey"
				subs["$stateKey"] = *event.StateKey
			} else {
				path = "/_matrix/client/v3/rooms/$roomId/send/$eventType/$txnId"
				subs["$txnId"] = fmt.Sprintf("%d", eventIndex)
			}

			// special cases: room joining, leaving and inviting
			if event.Type == "m.room.member" && event.StateKey != nil &&
				event.Content != nil && event.Content["membership"] != nil {
				membership, ok := event.Content["membership"].(string)
				if ok {
					switch membership {
					case "join":
						path = "/_matrix/client/v3/join/$roomId"
						method = "POST"

						// Set server_name to the homeserver that created the room, as they're a pretty
						// good candidate to join the room through
						queryParams["server_name"] = fmt.Sprintf(".room_ref_%s_server_name", room.Ref)
					case "leave":
						path = "/_matrix/client/v3/rooms/$roomId/leave"
						method = "POST"
						if *event.StateKey != event.Sender {
							// it's a kick
							path = "/_matrix/client/v3/rooms/$roomId/kick"
							method = "POST"
							event.Content["user_id"] = *event.StateKey
						}
					case "invite":
						path = "/_matrix/client/v3/rooms/$roomId/invite"
						method = "POST"
						event.Content["user_id"] = *event.StateKey
					}
				}
			} else if event.Type == "m.room.canonical_alias" && event.StateKey != nil &&
				*event.StateKey == "" {
				// create the alias first then send the canonical alias
				// keep a ref to the current room index so it's correct when bodyFn is called
				alias, ok := event.Content["alias"].(string)
				if ok {
					ri := roomIndex
					instrs = append(instrs, instruction{
						method:        "PUT",
						path:          "/_matrix/client/v3/directory/room/" + url.PathEscape(alias),
						accessToken:   fmt.Sprintf("user_%s", event.Sender),
						substitutions: subs,
						queryParams:   queryParams,
						bodyFn: func(lk *sync.Map) interface{} {
							val, _ := lk.Load(fmt.Sprintf("room_%d", ri))
							return map[string]interface{}{
								"room_id": val,
							}
						},
					})
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
		path:        "/_matrix/client/v3/register",
		accessToken: "",
		body:        body,
		storeResponse: map[string]string{
			"user_@" + user.Localpart + ":" + hs.Name:   ".access_token",
			"device_@" + user.Localpart + ":" + hs.Name: ".device_id",
		},
	}
}

func instructionDisplayName(hs b.Homeserver, user b.User) instruction {
	body := map[string]interface{}{
		"displayname": user.DisplayName,
	}
	return instruction{
		method: "PUT",
		path: fmt.Sprintf(
			"/_matrix/client/v3/profile/@%s:%s/displayname",
			user.Localpart, hs.Name,
		),
		accessToken: fmt.Sprintf("user_@%s:%s", user.Localpart, hs.Name),
		body:        body,
	}
}

func instructionLogin(hs b.Homeserver, user b.User) instruction {
	body := map[string]interface{}{
		"type":     "m.login.password",
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
		path:        "/_matrix/client/v3/login",
		accessToken: "",
		body:        body,
		storeResponse: map[string]string{
			"user_@" + user.Localpart + ":" + hs.Name:   ".access_token",
			"device_@" + user.Localpart + ":" + hs.Name: ".device_id",
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
		path:        "/_matrix/client/v3/keys/upload",
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
