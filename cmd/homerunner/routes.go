package main

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/matrix-org/util"
)

func Routes(rt *Runtime, cfg *Config) http.Handler {
	mux := mux.NewRouter()
	mux.Path("/create").Methods("POST").HandlerFunc(
		util.WithCORSOptions(util.MakeJSONAPI(util.NewJSONRequestHandler(
			func(req *http.Request) util.JSONResponse {
				rc := ReqCreate{}
				if err := json.NewDecoder(req.Body).Decode(&rc); err != nil {
					return util.MessageResponse(400, "request body not JSON")
				}
				return RouteCreate(req.Context(), rt, &rc)
			},
		))),
	)
	mux.Path("/destroy").Methods("POST").HandlerFunc(
		util.WithCORSOptions(util.MakeJSONAPI(util.NewJSONRequestHandler(
			func(req *http.Request) util.JSONResponse {
				rc := ReqDestroy{}
				if err := json.NewDecoder(req.Body).Decode(&rc); err != nil {
					return util.MessageResponse(400, "request body not JSON")
				}
				return RouteDestroy(req.Context(), rt, &rc)
			},
		))),
	)
	return mux
}
