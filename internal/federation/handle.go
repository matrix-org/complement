package federation

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/util"
)

// MakeJoinRequestsHandler is the http.Handler implementation for the make_join part of
// HandleMakeSendJoinRequests.
func MakeJoinRequestsHandler(s *Server, w http.ResponseWriter, req *http.Request) {
	// Check federation signature
	fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
		req, time.Now(), gomatrixserverlib.ServerName(s.ServerName), s.keyRing,
	)
	if fedReq == nil {
		w.WriteHeader(errResp.Code)
		b, _ := json.Marshal(errResp.JSON)
		w.Write(b)
		return
	}

	vars := mux.Vars(req)
	userID := vars["userID"]
	roomID := vars["roomID"]

	room, ok := s.rooms[roomID]
	if !ok {
		w.WriteHeader(404)
		w.Write([]byte("complement: HandleMakeSendJoinRequests make_join unexpected room ID: " + roomID))
		return
	}

	// Generate a join event
	builder := gomatrixserverlib.EventBuilder{
		Sender:     userID,
		RoomID:     roomID,
		Type:       "m.room.member",
		StateKey:   &userID,
		PrevEvents: []string{room.Timeline[len(room.Timeline)-1].EventID()},
	}
	err := builder.SetContent(map[string]interface{}{"membership": gomatrixserverlib.Join})
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte("complement: HandleMakeSendJoinRequests make_join cannot set membership content: " + err.Error()))
		return
	}
	stateNeeded, err := gomatrixserverlib.StateNeededForEventBuilder(&builder)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte("complement: HandleMakeSendJoinRequests make_join cannot calculate auth_events: " + err.Error()))
		return
	}
	builder.AuthEvents = room.AuthEvents(stateNeeded)

	// Send it
	res := map[string]interface{}{
		"event":        builder,
		"room_version": room.Version,
	}
	w.WriteHeader(200)
	b, _ := json.Marshal(res)
	w.Write(b)
}

// SendJoinRequestsHandler is the http.Handler implementation for the send_join part of
// HandleMakeSendJoinRequests.
func SendJoinRequestsHandler(s *Server, w http.ResponseWriter, req *http.Request) {
	fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
		req, time.Now(), gomatrixserverlib.ServerName(s.ServerName), s.keyRing,
	)
	if fedReq == nil {
		w.WriteHeader(errResp.Code)
		b, _ := json.Marshal(errResp.JSON)
		w.Write(b)
		return
	}
	vars := mux.Vars(req)
	roomID := vars["roomID"]

	room, ok := s.rooms[roomID]
	if !ok {
		w.WriteHeader(404)
		w.Write([]byte("complement: HandleMakeSendJoinRequests send_join unexpected room ID: " + roomID))
		return
	}
	event, err := gomatrixserverlib.NewEventFromUntrustedJSON(fedReq.Content(), room.Version)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte("complement: HandleMakeSendJoinRequests send_join cannot parse event JSON: " + err.Error()))
	}
	// insert the join event into the room state
	room.AddEvent(&event)

	// return current state and auth chain
	b, err := json.Marshal(gomatrixserverlib.RespSendJoin{
		AuthEvents:  room.AuthChain(),
		StateEvents: room.AllCurrentState(),
		Origin:      gomatrixserverlib.ServerName(s.ServerName),
	})
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte("complement: HandleMakeSendJoinRequests send_join cannot marshal RespSendJoin: " + err.Error()))
	}
	w.WriteHeader(200)
	w.Write(b)
}

// HandleMakeSendJoinRequests is an option which will process make_join and send_join requests for rooms which are present
// in this server. To add a room to this server, see Server.MustMakeRoom. No checks are done to see whether join requests
// are allowed or not. If you wish to test that, write your own test.
func HandleMakeSendJoinRequests() func(*Server) {
	return func(s *Server) {
		s.mux.Handle("/_matrix/federation/v1/make_join/{roomID}/{userID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			MakeJoinRequestsHandler(s, w, req)
		})).Methods("GET")

		s.mux.Handle("/_matrix/federation/v2/send_join/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			SendJoinRequestsHandler(s, w, req)
		})).Methods("PUT")
	}
}

// HandleDirectoryLookups will automatically return room IDs for any aliases present on this server.
func HandleDirectoryLookups() func(*Server) {
	return func(s *Server) {
		if s.directoryHandlerSetup {
			return
		}
		s.directoryHandlerSetup = true
		s.mux.Handle("/_matrix/federation/v1/query/directory", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			alias := req.URL.Query().Get("room_alias")
			if roomID, ok := s.aliases[alias]; ok {
				b, err := json.Marshal(gomatrixserverlib.RespDirectory{
					RoomID: roomID,
					Servers: []gomatrixserverlib.ServerName{
						gomatrixserverlib.ServerName(s.ServerName),
					},
				})
				if err != nil {
					w.WriteHeader(500)
					w.Write([]byte("complement: HandleDirectoryLookups failed to marshal JSON: " + err.Error()))
					return
				}
				w.WriteHeader(200)
				w.Write(b)
				return
			}
			w.WriteHeader(404)
			w.Write([]byte(`{
				"errcode": "M_NOT_FOUND",
				"error": "Room alias not found."
			}`))
		})).Methods("GET")
	}
}

// HandleEventRequests is an option which will process GET /_matrix/federation/v1/event/{eventId} requests universally when requested.
func HandleEventRequests() func(*Server) {
	return func(srv *Server) {
		srv.mux.Handle("/_matrix/federation/v1/event/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			vars := mux.Vars(req)
			eventID := vars["eventID"]
			var event *gomatrixserverlib.Event
			// find the event
		RoomLoop:
			for _, room := range srv.rooms {
				for _, ev := range room.Timeline {
					if ev.EventID() == eventID {
						event = ev
						break RoomLoop
					}
				}
			}

			txn := gomatrixserverlib.Transaction{
				Origin:         gomatrixserverlib.ServerName(srv.ServerName),
				OriginServerTS: gomatrixserverlib.AsTimestamp(time.Now()),
				PDUs: []json.RawMessage{
					event.JSON(),
				},
			}
			resp, err := json.Marshal(txn)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte(fmt.Sprintf(`complement: failed to marshal JSON response: %s`, err)))
				return
			}
			w.WriteHeader(200)
			w.Write(resp)
		}))
	}
}

// HandleKeyRequests is an option which will process GET /_matrix/key/v2/server requests universally when requested.
func HandleKeyRequests() func(*Server) {
	return func(srv *Server) {
		keymux := srv.mux.PathPrefix("/_matrix/key/v2").Subrouter()

		certData, err := ioutil.ReadFile(srv.certPath)
		if err != nil {
			panic("failed to read cert file: " + err.Error())
		}

		keyFn := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			k := gomatrixserverlib.ServerKeys{}
			k.ServerName = gomatrixserverlib.ServerName(srv.ServerName)
			publicKey := srv.Priv.Public().(ed25519.PublicKey)
			k.VerifyKeys = map[gomatrixserverlib.KeyID]gomatrixserverlib.VerifyKey{
				srv.KeyID: {
					Key: gomatrixserverlib.Base64Bytes(publicKey),
				},
			}
			k.OldVerifyKeys = map[gomatrixserverlib.KeyID]gomatrixserverlib.OldVerifyKey{}
			k.TLSFingerprints = fingerprintPEM(certData)
			k.ValidUntilTS = gomatrixserverlib.AsTimestamp(time.Now().Add(24 * time.Hour))
			toSign, err := json.Marshal(k.ServerKeyFields)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte("complement: HandleKeyRequests cannot marshal serverkeyfields: " + err.Error()))
				return
			}

			k.Raw, err = gomatrixserverlib.SignJSON(
				string(srv.ServerName), srv.KeyID, srv.Priv, toSign,
			)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte("complement: HandleKeyRequests cannot sign json: " + err.Error()))
				return
			}
			w.WriteHeader(200)
			w.Write(k.Raw)
		})

		keymux.Handle("/server", keyFn).Methods("GET")
		keymux.Handle("/server/", keyFn).Methods("GET")
		keymux.Handle("/server/{keyID}", keyFn).Methods("GET")
	}
}

// HandleMediaRequests is an option which will process /_matrix/media/v1/download/* using the provided map
// as a way to do so. The key of the map is the media ID to be handled.
func HandleMediaRequests(mediaIds map[string]func(w http.ResponseWriter)) func(*Server) {
	return func(srv *Server) {
		mediamux := srv.mux.PathPrefix("/_matrix/media").Subrouter()

		downloadFn := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			vars := mux.Vars(req)
			origin := vars["origin"]
			mediaId := vars["mediaId"]

			if origin != srv.ServerName {
				w.WriteHeader(400)
				w.Write([]byte("complement: Invalid Origin; Expected " + srv.ServerName))
				return
			}

			if f, ok := mediaIds[mediaId]; ok {
				f(w)
			} else {
				w.WriteHeader(404)
				w.Write([]byte("complement: Unknown predefined media ID: " + mediaId))
				return
			}
		})

		// Note: The spec says to use r0, but implementations rely on /v1 working for federation requests as a legacy
		// route.
		mediamux.Handle("/v1/download/{origin}/{mediaId}", downloadFn).Methods("GET")
		mediamux.Handle("/r0/download/{origin}/{mediaId}", downloadFn).Methods("GET")
	}
}

// HandleTransactionRequests is an option which will process GET /_matrix/federation/v1/send/{transactionID} requests universally when requested.
func HandleTransactionRequests() func(*Server) {
	return func(srv *Server) {
		srv.mux.Handle("/_matrix/federation/v1/send/{transactionID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Check federation signature
			fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
				req, time.Now(), gomatrixserverlib.ServerName(srv.ServerName), srv.keyRing,
			)
			if fedReq == nil {
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			// Read the request body
			body, err := ioutil.ReadAll(req.Body)
			if err != nil {
				errResp := util.MessageResponse(400, err.Error())
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			// Unmarshal the request body into a transaction object
			var transaction gomatrixserverlib.Transaction
			err = json.Unmarshal(body, &transaction)
			if err != nil {
				errResp := util.MessageResponse(400, err.Error())
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			// Extract the transaction ID from the request vars
			vars := mux.Vars(req)
			transaction.TransactionID = gomatrixserverlib.TransactionID(vars["transactionID"])

			// Transactions are limited in size; they can have at most 50 PDUs and 100 EDUs.
			// https://matrix.org/docs/spec/server_server/latest#transactions
			if len(transaction.PDUs) > 50 || len(transaction.EDUs) > 100 {
				errResp := util.MessageResponse(400, "Transactions are limited to 50 PDUs and 100 EDUs")
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			// Construct a response and fill as we process each PDU
			response := gomatrixserverlib.RespSend{}
			response.PDUs = make(map[string]gomatrixserverlib.PDUResult, 0)
			for _, pdu := range transaction.PDUs {
				var header struct {
					RoomID string `json:"room_id"`
				}
				if err := json.Unmarshal(pdu, &header); err != nil {
					log.Printf("Transaction '%s': Failed to extract room ID from event", transaction.TransactionID)
					// We don't know the event ID at this point so we can't return the
					// failure in the PDU results
				}

				// Retrieve the room version from the server
				room := srv.rooms[header.RoomID]
				roomVersion := gomatrixserverlib.RoomVersion(room.Version)

				event, err := gomatrixserverlib.NewEventFromUntrustedJSON(pdu, roomVersion)
				if err != nil {
					// We were unable to verify or process this event.
					// Add this PDU to the response along with an error message.
					response.PDUs[event.EventID()] = gomatrixserverlib.PDUResult{
						Error: err.Error(),
					}
					continue
				}

				// Store this PDU in the room's timeline
				room.Timeline = append(room.Timeline, &event)

				// Add this PDU as a success to the response
				response.PDUs[event.EventID()] = gomatrixserverlib.PDUResult{}
			}

			// TODO: Handle EDUs

			resp, err := json.Marshal(response)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte(fmt.Sprintf(`complement: failed to marshal JSON response: %s`, err)))
				return
			}
			w.WriteHeader(200)
			w.Write(resp)
		})).Methods("PUT")
	}
}
