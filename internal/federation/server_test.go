package federation

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/internal/config"
	"github.com/matrix-org/complement/internal/docker"
)

func TestComplementServerIsSigned(t *testing.T) {
	docker.HostnameRunningComplement = "localhost"
	cfg := config.NewConfigFromEnvVars("test", "unimportant")
	srv := NewServer(t, &docker.Deployment{
		Config: cfg,
	})
	srv.UnexpectedRequestsAreErrors = false
	cancel := srv.Listen()
	t.Logf("Listening on %s", srv.serverName)
	defer cancel()

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(cfg.CACertificate)
	tlsConfig := &tls.Config{
		RootCAs: caCertPool,
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	client := &http.Client{Transport: transport}

	resp, err := client.Get("https://" + srv.ServerName())
	if err != nil {
		t.Fatalf("Failed to GET: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
