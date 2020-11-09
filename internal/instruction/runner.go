package instruction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/matrix-org/complement/internal/b"
	"github.com/tidwall/gjson"
)

type Runner struct {
	blueprintName string
	lookup        map[string]string
	debugLogging  bool
}

func NewRunner(blueprintName string, debugLogging bool) *Runner {
	return &Runner{
		lookup:        make(map[string]string),
		blueprintName: blueprintName,
		debugLogging:  debugLogging,
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
	for key, val := range r.lookup {
		if strings.HasPrefix(key, "user_@") && strings.HasSuffix(key, ":"+hsDomain) {
			res[strings.TrimPrefix(key, "user_")] = val
		}
	}
	return res
}

// Run all instructions until completion. Return an error if there was a problem executing any instruction.
func (r *Runner) Run(hs b.Homeserver, hsURL string) error {
	contextStr := fmt.Sprintf("%s.%s", r.blueprintName, hs.Name)
	instrs := calculateInstructions(hs)
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
		r.log("%s [%s] %s => HTTP %s\n", contextStr, instr.accessToken, req.URL.String(), res.Status)
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
				r.lookup[k] = val.Str
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
		q.Set("access_token", r.lookup[instr.accessToken])
	}
	for paramName, paramValue := range instr.queryParams {
		q.Set(paramName, paramValue)
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

// url returns the complete resolved url for this instruction,
func (i *instruction) url(hsURL string, lookup map[string]string) string {
	pathTemplate := i.path
	for k, v := range i.substitutions {
		// if the variable start with a '.' then use the lookup table, else use the string literally
		// this handles scenarios like:
		// { $roomId: ".room_0", $eventType: "m.room.message" }
		var valToEncode string
		if v[0] == '.' {
			valToEncode = lookup[strings.TrimPrefix(v, ".")]
		} else {
			valToEncode = v
		}
		pathTemplate = strings.Replace(pathTemplate, k, url.PathEscape(valToEncode), -1)
	}
	return hsURL + pathTemplate
}

// calculateInstructions returns the entire set of HTTP requests to be executed in order. Various substitutions
// and placeholders are returned in these instructions as it's impossible to know at this time what room IDs etc
// will be allocated, so use an instruction loader to load the right requests.
func calculateInstructions(hs b.Homeserver) []instruction {
	var instrs []instruction
	// add instructions to create users
	for _, user := range hs.Users {
		instrs = append(instrs, instruction{
			method:      "POST",
			path:        "/_matrix/client/r0/register",
			accessToken: "",
			body: map[string]interface{}{
				"username": user.Localpart,
				"password": "complement_meets_min_pasword_req_" + user.Localpart,
				"auth": map[string]string{
					"type": "m.login.dummy",
				},
			},
			storeResponse: map[string]string{
				"user_@" + user.Localpart + ":" + hs.Name: ".access_token",
			},
		})
	}
	// add instructions to create rooms and send events
	for roomIndex, room := range hs.Rooms {
		if room.Creator != "" {
			storeRes := map[string]string{
				fmt.Sprintf("room_%d", roomIndex): ".room_id",
			}
			if room.Ref != "" {
				storeRes[fmt.Sprintf("room_ref_%s", room.Ref)] = ".room_id"
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
				}
			}
			instrs = append(instrs, instruction{
				method:        method,
				path:          path,
				body:          event.Content,
				accessToken:   fmt.Sprintf("user_%s", event.Sender),
				substitutions: subs,
			})
		}
	}

	return instrs
}
