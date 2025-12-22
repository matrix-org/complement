package main

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/matrix-org/util"
)

func Routes(rt *Runtime, cfg *Config) http.Handler {
	mux := mux.NewRouter()
	mux.Path("/create").Methods("POST", "OPTIONS").HandlerFunc(
		withCORS(util.MakeJSONAPI(util.NewJSONRequestHandler(
			func(req *http.Request) util.JSONResponse {
				rc := ReqCreate{}
				if err := json.NewDecoder(req.Body).Decode(&rc); err != nil {
					return util.MessageResponse(400, "request body not JSON")
				}
				return RouteCreate(req.Context(), rt, &rc)
			},
		))),
	)
	mux.Path("/destroy").Methods("POST", "OPTIONS").HandlerFunc(
		withCORS(util.MakeJSONAPI(util.NewJSONRequestHandler(
			func(req *http.Request) util.JSONResponse {
				rc := ReqDestroy{}
				if err := json.NewDecoder(req.Body).Decode(&rc); err != nil {
					return util.MessageResponse(400, "request body not JSON")
				}
				return RouteDestroy(req.Context(), rt, &rc)
			},
		))),
	)
	mux.Path("/health").Methods("GET", "OPTIONS").HandlerFunc(
		withCORS(func(res http.ResponseWriter, req *http.Request) {
			res.WriteHeader(200)
		}),
	)
	return mux
}

// withCORS intercepts all requests and adds CORS headers.
func withCORS(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		util.SetCORSHeaders(w)
		handler(w, req)
	}
}
