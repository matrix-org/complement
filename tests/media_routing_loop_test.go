package tests

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/matrix-org/complement/internal/b"
	"github.com/matrix-org/complement/internal/client"
	"github.com/matrix-org/complement/internal/docker"
)

// sytest: Alternative server names do not cause a routing loop
func TestMediaRoutingLoop(t *testing.T) {
	deployment := Deploy(t, b.BlueprintAlice)
	defer deployment.Destroy(t)

	hsAddr, err := net.ResolveTCPAddr("tcp", deployment.HS["hs1"].FedBaseURL[8:])
	if err != nil {
		t.Fatalf("Could not create remote hs address: %s", err)
	}

	proxy, cancel := createProxyListener(t, hsAddr)
	defer cancel()

	proxyAddr := fmt.Sprintf("%s:%d", docker.HostnameRunningComplement, proxy.LocalAddr().Port)

	alice := deployment.Client(t, "hs1", "@alice:hs1")

	fileBody := []byte("hello")

	mxcUri := alice.UploadContent(t, fileBody, "hello", "text/plain")

	_, mediaId := client.SplitMxc(mxcUri)

	res := alice.DoFunc(t, "GET", []string{"_matrix", "media", "r0", "download", proxyAddr, mediaId})
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != 404 {
		t.Fatalf("Response does not have 404 code")
	}
}

func createProxyListener(t *testing.T, remote *net.TCPAddr) (*tcpProxy, func()) {
	local, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.Fatalf("Could not create local listener address: %s", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	listener, err := net.ListenTCP("tcp", local)
	if err != nil {
		t.Fatalf("Could not create local listener: %s", err)
	}

	p := &tcpProxy{ctx: ctx, listener: listener, remoteAddr: remote, pending: make(chan *net.TCPConn)}

	go p.Listen()

	return p, cancel
}

type tcpProxy struct {
	listener   *net.TCPListener
	remoteAddr *net.TCPAddr
	pending    chan *net.TCPConn
	ctx        context.Context
}

func (p *tcpProxy) LocalAddr() *net.TCPAddr {
	// extremely scuffed, but this is the only way to resolve it back
	addr, err := net.ResolveTCPAddr("tcp", p.listener.Addr().String())
	if err != nil {
		panic(err)
	}
	return addr
}

func (p *tcpProxy) Listen() {
	go p.Handle()

	for {
		conn, err := p.listener.AcceptTCP()
		if err != nil {
			close(p.pending)
		}
		p.pending <- conn
	}
}

func (p *tcpProxy) Handle() {
	for conn := range p.pending {
		go p.Proxy(conn)
	}
}

func (p *tcpProxy) Proxy(conn *net.TCPConn) {
	defer conn.Close()

	rConn, err := net.DialTCP("tcp", nil, p.remoteAddr)
	if err != nil {
		panic(err)
	}
	defer rConn.Close()

	sendDone := make(chan bool)
	recvDone := make(chan bool)

	go func() {
		io.Copy(conn, rConn)
		sendDone <- true
	}()

	go func() {
		io.Copy(rConn, conn)
		recvDone <- true
	}()

	select {
	case <-recvDone:
	case <-sendDone:
	case <-p.ctx.Done():
		return
	}
}
