package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/gorilla/mux"

	"github.com/matrix-org/complement/internal/config"
)

type Server struct {
	Port     int
	server   *http.Server
	listener net.Listener
}

func NewServer(t *testing.T, configFunc func(router *mux.Router)) *Server {
	t.Helper()

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("Could not create listener for web server: %s", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port

	r := mux.NewRouter()

	configFunc(r)

	server := &http.Server{Addr: ":0", Handler: r}

	go server.Serve(listener)

	return &Server{
		Port:     port,
		server:   server,
		listener: listener,
	}
}

func (s *Server) Close() {
	s.server.Shutdown(context.Background())
	s.listener.Close()
}

// Url returns the formatted URL that homeservers can access the server with, without leading slash
func (s *Server) Url(comp *config.Complement) string {
	return fmt.Sprintf("http://%s:%d", comp.HostnameRunningComplement, s.Port)
}
