package federation

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
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
		req, time.Now(), gomatrixserverlib.ServerName(s.serverName), s.keyRing,
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

	makeJoinResp, err := MakeRespMakeJoin(s, room, userID)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(fmt.Sprintf("complement: HandleMakeSendJoinRequests %s", err)))
		return
	}

	// Send it
	w.WriteHeader(200)
	b, _ := json.Marshal(makeJoinResp)
	w.Write(b)
}

// MakeRespMakeJoin makes the response for a /make_join request, without verifying any signatures
// or dealing with HTTP responses itself.
func MakeRespMakeJoin(s *Server, room *ServerRoom, userID string) (resp gomatrixserverlib.RespMakeJoin, err error) {
	// Generate a join event
	builder := gomatrixserverlib.EventBuilder{
		Sender:     userID,
		RoomID:     room.RoomID,
		Type:       "m.room.member",
		StateKey:   &userID,
		PrevEvents: []string{room.Timeline[len(room.Timeline)-1].EventID()},
		Depth:      room.Timeline[len(room.Timeline)-1].Depth() + 1,
	}
	err = builder.SetContent(map[string]interface{}{"membership": gomatrixserverlib.Join})
	if err != nil {
		err = fmt.Errorf("make_join cannot set membership content: %w", err)
		return
	}
	stateNeeded, err := gomatrixserverlib.StateNeededForEventBuilder(&builder)
	if err != nil {
		err = fmt.Errorf("make_join cannot calculate auth_events: %w", err)
		return
	}
	builder.AuthEvents = room.AuthEvents(stateNeeded)

	resp = gomatrixserverlib.RespMakeJoin{
		RoomVersion: room.Version,
		JoinEvent:   builder,
	}
	return
}

// MakeRespMakeKnock makes the response for a /make_knock request, without verifying any signatures
// or dealing with HTTP responses itself.
func MakeRespMakeKnock(s *Server, room *ServerRoom, userID string) (resp gomatrixserverlib.RespMakeKnock, err error) {
	// Generate a knock event
	builder := gomatrixserverlib.EventBuilder{
		Sender:     userID,
		RoomID:     room.RoomID,
		Type:       "m.room.member",
		StateKey:   &userID,
		PrevEvents: []string{room.Timeline[len(room.Timeline)-1].EventID()},
		Depth:      room.Timeline[len(room.Timeline)-1].Depth() + 1,
	}
	err = builder.SetContent(map[string]interface{}{"membership": gomatrixserverlib.Join})
	if err != nil {
		err = fmt.Errorf("make_knock cannot set membership content: %w", err)
		return
	}
	stateNeeded, err := gomatrixserverlib.StateNeededForEventBuilder(&builder)
	if err != nil {
		err = fmt.Errorf("make_knock cannot calculate auth_events: %w", err)
		return
	}
	builder.AuthEvents = room.AuthEvents(stateNeeded)

	resp = gomatrixserverlib.RespMakeKnock{
		RoomVersion: room.Version,
		KnockEvent:  builder,
	}
	return
}

// SendJoinRequestsHandler is the http.Handler implementation for the send_join part of
// HandleMakeSendJoinRequests.
//
// expectPartialState should be true if we should expect the incoming send_join
// request to use the partial_state flag, per MSC3706. In that case, we reply
// with only the critical subset of the room state.
func SendJoinRequestsHandler(s *Server, w http.ResponseWriter, req *http.Request, expectPartialState bool) {
	fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
		req, time.Now(), gomatrixserverlib.ServerName(s.serverName), s.keyRing,
	)
	if fedReq == nil {
		w.WriteHeader(errResp.Code)
		b, _ := json.Marshal(errResp.JSON)
		w.Write(b)
		return
	}

	// if we expect a partial-state join, the request should have a "partial_state" flag
	queryParams := req.URL.Query()
	partialState := queryParams.Get("org.matrix.msc3706.partial_state")
	if expectPartialState && partialState != "true" {
		log.Printf("Not a partial-state request: got %v, want %s",
			partialState, "true")
		w.WriteHeader(500)
		w.Write([]byte("complement: Incoming send_join was not partial_state"))
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
		return
	}

	// build the state list *before* we insert the new event
	var stateEvents []*gomatrixserverlib.Event
	for _, ev := range room.State {
		// filter out non-critical memberships if this is a partial-state join
		if expectPartialState {
			if ev.Type() == "m.room.member" && ev.StateKey() != event.StateKey() {
				continue
			}
		}
		stateEvents = append(stateEvents, ev)
	}

	authEvents := room.AuthChainForEvents(stateEvents)

	// get servers in room *before* the join event
	serversInRoom := room.ServersInRoom()

	// insert the join event into the room state
	room.AddEvent(event)
	log.Printf("Received send-join of event %s", event.EventID())

	// return state and auth chain
	b, err := json.Marshal(gomatrixserverlib.RespSendJoin{
		Origin:        gomatrixserverlib.ServerName(s.serverName),
		AuthEvents:    gomatrixserverlib.NewEventJSONsFromEvents(authEvents),
		StateEvents:   gomatrixserverlib.NewEventJSONsFromEvents(stateEvents),
		PartialState:  expectPartialState,
		ServersInRoom: serversInRoom,
	})
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte("complement: HandleMakeSendJoinRequests send_join cannot marshal RespSendJoin: " + err.Error()))
		return
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
			SendJoinRequestsHandler(s, w, req, false)
		})).Methods("PUT")
	}
}

// HandlePartialStateMakeSendJoinRequests is similar to HandleMakeSendJoinRequests, but expects a partial-state join.
func HandlePartialStateMakeSendJoinRequests() func(*Server) {
	return func(s *Server) {
		s.mux.Handle("/_matrix/federation/v1/make_join/{roomID}/{userID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			MakeJoinRequestsHandler(s, w, req)
		})).Methods("GET")

		s.mux.Handle("/_matrix/federation/v2/send_join/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			SendJoinRequestsHandler(s, w, req, true)
		})).Methods("PUT")
	}
}

// HandleInviteRequests is an option which makes the server process invite requests.
//
// inviteCallback is a callback function that if non-nil will be called and passed the incoming invite event
func HandleInviteRequests(inviteCallback func(*gomatrixserverlib.Event)) func(*Server) {
	return func(s *Server) {
		// https://matrix.org/docs/spec/server_server/r0.1.4#put-matrix-federation-v2-invite-roomid-eventid
		s.mux.Handle("/_matrix/federation/v2/invite/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
				req, time.Now(), gomatrixserverlib.ServerName(s.serverName), s.keyRing,
			)
			if fedReq == nil {
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			var inviteRequest gomatrixserverlib.InviteV2Request
			if err := json.Unmarshal(fedReq.Content(), &inviteRequest); err != nil {
				log.Printf(
					"complement: Unable to unmarshal incoming /invite request: %s",
					err.Error(),
				)

				errResp := util.MessageResponse(400, err.Error())
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			if inviteCallback != nil {
				inviteCallback(inviteRequest.Event())
			}

			// Sign the event before we send it back
			signedEvent := inviteRequest.Event().Sign(s.serverName, s.KeyID, s.Priv)

			// Send the response
			res := map[string]interface{}{
				"event": signedEvent,
			}
			w.WriteHeader(200)
			b, _ := json.Marshal(res)
			w.Write(b)
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
						gomatrixserverlib.ServerName(s.serverName),
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

			if event == nil {
				w.WriteHeader(404)
				w.Write([]byte(fmt.Sprintf(`complement: failed to find event: %s`, eventID)))
				return
			}

			txn := gomatrixserverlib.Transaction{
				Origin:         gomatrixserverlib.ServerName(srv.serverName),
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

// HandleEventAuthRequests is an option which will process GET /_matrix/federation/v1/event_auth/{roomId}/{eventId}
// requests universally when requested.
func HandleEventAuthRequests() func(*Server) {
	return func(srv *Server) {
		srv.mux.Handle("/_matrix/federation/v1/event_auth/{roomID}/{eventID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			vars := mux.Vars(req)
			roomID := vars["roomID"]
			eventID := vars["eventID"]

			room, ok := srv.rooms[roomID]
			if !ok {
				srv.t.Logf("/event_auth request for unknown room ID %s", roomID)
				w.WriteHeader(404)
				w.Write([]byte("complement: HandleEventAuthRequests event_auth unknown room ID: " + roomID))
				return
			}

			// find the event
			var event *gomatrixserverlib.Event
			for _, ev := range room.Timeline {
				if ev.EventID() == eventID {
					event = ev
					break
				}
			}

			if event == nil {
				srv.t.Logf("/event_auth request for unknown event ID %s in room %s", eventID, roomID)
				w.WriteHeader(404)
				w.Write([]byte("complement: HandleEventAuthRequests event_auth unknown event ID: " + eventID))
				return
			}

			authEvents := room.AuthChainForEvents([]*gomatrixserverlib.Event{event})
			resp := gomatrixserverlib.RespEventAuth{
				gomatrixserverlib.NewEventJSONsFromEvents(authEvents),
			}
			respJSON, err := json.Marshal(resp)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte(fmt.Sprintf(`complement: failed to marshal JSON response: %s`, err)))
				return
			}
			w.WriteHeader(200)
			w.Write(respJSON)
		}))
	}
}

// HandleKeyRequests is an option which will process GET /_matrix/key/v2/server requests universally when requested.
func HandleKeyRequests() func(*Server) {
	return func(srv *Server) {
		keymux := srv.mux.PathPrefix("/_matrix/key/v2").Subrouter()
		keyFn := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			k := gomatrixserverlib.ServerKeys{}
			k.ServerName = gomatrixserverlib.ServerName(srv.serverName)
			publicKey := srv.Priv.Public().(ed25519.PublicKey)
			k.VerifyKeys = map[gomatrixserverlib.KeyID]gomatrixserverlib.VerifyKey{
				srv.KeyID: {
					Key: gomatrixserverlib.Base64Bytes(publicKey),
				},
			}
			k.OldVerifyKeys = map[gomatrixserverlib.KeyID]gomatrixserverlib.OldVerifyKey{}
			k.ValidUntilTS = gomatrixserverlib.AsTimestamp(time.Now().Add(24 * time.Hour))
			toSign, err := json.Marshal(k.ServerKeyFields)
			if err != nil {
				w.WriteHeader(500)
				w.Write([]byte("complement: HandleKeyRequests cannot marshal serverkeyfields: " + err.Error()))
				return
			}

			k.Raw, err = gomatrixserverlib.SignJSON(
				string(srv.serverName), srv.KeyID, srv.Priv, toSign,
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

			if origin != srv.serverName {
				w.WriteHeader(400)
				w.Write([]byte("complement: Invalid Origin; Expected " + srv.serverName))
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

		// Note: The spec says to use /v3, but implementations rely on /v1 and /r0 working for federation requests as a legacy
		// route.
		mediamux.Handle("/r0/download/{origin}/{mediaId}", downloadFn).Methods("GET")
		mediamux.Handle("/v1/download/{origin}/{mediaId}", downloadFn).Methods("GET")
		mediamux.Handle("/v3/download/{origin}/{mediaId}", downloadFn).Methods("GET")
	}
}

// HandleTransactionRequests is an option which will process GET /_matrix/federation/v1/send/{transactionID} requests universally when requested.
// pduCallback and eduCallback are functions that if non-nil will be called and passed each PDU or EDU event received in the transaction.
// Callbacks will be fired AFTER the event has been stored onto the respective ServerRoom.
func HandleTransactionRequests(pduCallback func(*gomatrixserverlib.Event), eduCallback func(gomatrixserverlib.EDU)) func(*Server) {
	return func(srv *Server) {
		srv.mux.Handle("/_matrix/federation/v1/send/{transactionID}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Extract the transaction ID from the request vars
			vars := mux.Vars(req)
			transactionID := vars["transactionID"]

			// Check federation signature
			fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
				req, time.Now(), gomatrixserverlib.ServerName(srv.serverName), srv.keyRing,
			)
			if fedReq == nil {
				log.Printf(
					"complement: Transaction '%s': HTTP Code %d. Invalid http request: %s",
					transactionID, errResp.Code, errResp.JSON,
				)

				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			// Unmarshal the request body into a transaction object
			var transaction gomatrixserverlib.Transaction
			err := json.Unmarshal(fedReq.Content(), &transaction)
			if err != nil {
				log.Printf(
					"complement: Transaction '%s': Unable to unmarshal transaction body bytes into Transaction object: %s",
					transaction.TransactionID, err.Error(),
				)

				errResp := util.MessageResponse(400, err.Error())
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}
			transaction.TransactionID = gomatrixserverlib.TransactionID(transactionID)

			// Transactions are limited in size; they can have at most 50 PDUs and 100 EDUs.
			// https://matrix.org/docs/spec/server_server/latest#transactions
			if len(transaction.PDUs) > 50 || len(transaction.EDUs) > 100 {
				log.Printf(
					"complement: Transaction '%s': Transaction too large. PDUs: %d/50, EDUs: %d/100",
					transaction.TransactionID, len(transaction.PDUs), len(transaction.EDUs),
				)

				errResp := util.MessageResponse(400, "Transactions are limited to 50 PDUs and 100 EDUs")
				w.WriteHeader(errResp.Code)
				b, _ := json.Marshal(errResp.JSON)
				w.Write(b)
				return
			}

			// Construct a response and fill as we process each PDU
			response := gomatrixserverlib.RespSend{}
			response.PDUs = make(map[string]gomatrixserverlib.PDUResult)
			for _, pdu := range transaction.PDUs {
				var header struct {
					RoomID string `json:"room_id"`
				}
				if err = json.Unmarshal(pdu, &header); err != nil {
					log.Printf("complement: Transaction '%s': Failed to extract room ID from event: %s", transaction.TransactionID, err.Error())

					// We don't know the event ID at this point so we can't return the
					// failure in the PDU results
					continue
				}

				// Retrieve the room version from the server
				room := srv.rooms[header.RoomID]
				if room == nil {
					// An invalid room ID may have been provided
					log.Printf("complement: Transaction '%s': Failed to find local room: %s", transaction.TransactionID, header.RoomID)
					continue
				}
				roomVersion := gomatrixserverlib.RoomVersion(room.Version)

				var event *gomatrixserverlib.Event
				event, err = gomatrixserverlib.NewEventFromUntrustedJSON(pdu, roomVersion)
				if err != nil {
					// We were unable to verify or process this event.
					log.Printf(
						"complement: Transaction '%s': Unable to process event '%s': %s",
						transaction.TransactionID, event.EventID(), err.Error(),
					)

					// We still don't know the event ID, and cannot add the failure to the PDU results
					continue
				}

				// Store this PDU in the room's timeline
				room.AddEvent(event)

				// Add this PDU as a success to the response
				response.PDUs[event.EventID()] = gomatrixserverlib.PDUResult{}

				// Run the PDU callback function with this event
				if pduCallback != nil {
					pduCallback(event)
				}
			}

			for _, edu := range transaction.EDUs {
				// Run the EDU callback function with this EDU
				if eduCallback != nil {
					eduCallback(edu)
				}
			}

			resp, err := json.Marshal(response)
			if err != nil {
				log.Printf("complement: Transaction '%s': Failed to marshal JSON response: %s", transaction.TransactionID, err.Error())
				w.WriteHeader(500)
				w.Write([]byte(fmt.Sprintf(`complement: failed to marshal JSON response: %s`, err)))
				return
			}
			w.WriteHeader(200)
			w.Write(resp)
		})).Methods("PUT")
	}
}
